package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/supabase-community/supabase-go"
)

const weatherURL = "https://api.open-meteo.com/v1/forecast"

type Config struct {
	Port, SensorURL, SensorToken string
	SupabaseURL, SupabaseKey     string

	FixSensorTimestamp bool

	SupabaseTable, UserColumn, DevicesColumn string
	Dry, Wet, RainProbability, MinRainMM     float64
	KalmanQ, KalmanR                         float64

	// Parâmetros do modelo preditivo de nível + tendência.
	PredictionHorizonHours int
	PredictionStepMinutes  int
	PredictionProcessNoise float64
	SoilMoistureMin        float64
	SoilMoistureMax        float64

	// Critérios de diagnóstico calculados exclusivamente pelo MSS.
	TrendStableThreshold float64
	HighUncertaintyWidth float64
	DiagnosticMinSamples int
	SoilZeroTolerance    float64
	SoilSaturationMargin float64

	Timeout, SensorMaxAge time.Duration
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func number(key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(os.Getenv(key), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

func wholeNumber(key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return v
}

func config() (Config, error) {
	c := Config{
		FixSensorTimestamp: 
		// strings.EqualFold(
			// env("TEMP_FIX_SENSOR_TIMESTAMP", "false"),
			true,
		// ),
		Port:            env("PORT", "8456"),
		SensorURL:       strings.TrimSpace(os.Getenv("SENSOR_API_URL_TEMPLATE")),
		SensorToken:     os.Getenv("SENSOR_API_TOKEN"),
		SupabaseURL:     strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseKey:     os.Getenv("SUPABASE_KEY"),
		SupabaseTable:   env("SUPABASE_TABLE", "users"),
		UserColumn:      env("SUPABASE_USER_COLUMN", "userId"),
		DevicesColumn:   env("SUPABASE_DEVICES_COLUMN", "devices"),
		Dry:             number("SOIL_DRY_THRESHOLD", 30),
		Wet:             number("SOIL_WET_THRESHOLD", 70),
		RainProbability: number("RAIN_PROBABILITY_THRESHOLD", 60),
		MinRainMM:       number("MIN_RAIN_MM", 3),
		KalmanQ:         number("KALMAN_PROCESS_NOISE", 1),
		KalmanR:         number("KALMAN_MEASUREMENT_NOISE", 4),

		PredictionHorizonHours: wholeNumber("SOIL_PREDICTION_HORIZON_HOURS", 24),
		PredictionStepMinutes:  wholeNumber("SOIL_PREDICTION_STEP_MINUTES", 60),
		PredictionProcessNoise: number("SOIL_PREDICTION_PROCESS_NOISE", 0.05),
		SoilMoistureMin:        number("SOIL_MOISTURE_MIN_VALUE", 0),
		SoilMoistureMax:        number("SOIL_MOISTURE_MAX_VALUE", 100),

		TrendStableThreshold: number("SOIL_TREND_STABLE_THRESHOLD", 0.1),
		HighUncertaintyWidth: number("SOIL_PREDICTION_HIGH_UNCERTAINTY_WIDTH", 30),
		DiagnosticMinSamples: wholeNumber("SOIL_DIAGNOSTIC_CONSECUTIVE_SAMPLES", 5),
		SoilZeroTolerance:    number("SOIL_ZERO_TOLERANCE", 0.5),
		SoilSaturationMargin: number("SOIL_SATURATION_MARGIN", 1),

		Timeout: 8 * time.Second, SensorMaxAge: 24 * time.Hour,
	}
	if c.Port == "" || c.SensorURL == "" || c.SupabaseURL == "" || c.SupabaseKey == "" {
		return c, errors.New("configure PORT, SENSOR_API_URL_TEMPLATE, SUPABASE_URL e SUPABASE_KEY")
	}
	if !strings.Contains(c.SensorURL, "{user}") || !strings.Contains(c.SensorURL, "{devEUI}") {
		return c, errors.New("SENSOR_API_URL_TEMPLATE deve conter {user} e {devEUI}")
	}
	if c.Dry < 0 || c.Wet <= c.Dry || c.RainProbability < 0 || c.RainProbability > 100 || c.MinRainMM < 0 || c.KalmanQ < 0 || c.KalmanR <= 0 {
		return c, errors.New("limiares inválidos: confira SOIL_DRY_THRESHOLD, SOIL_WET_THRESHOLD, RAIN_PROBABILITY_THRESHOLD, MIN_RAIN_MM, KALMAN_PROCESS_NOISE e KALMAN_MEASUREMENT_NOISE")
	}
	if c.PredictionHorizonHours < 1 || c.PredictionHorizonHours > 168 {
		return c, errors.New("SOIL_PREDICTION_HORIZON_HOURS deve estar entre 1 e 168")
	}
	if c.PredictionStepMinutes < 5 || c.PredictionStepMinutes > 1440 {
		return c, errors.New("SOIL_PREDICTION_STEP_MINUTES deve estar entre 5 e 1440")
	}
	if c.PredictionProcessNoise < 0 || c.SoilMoistureMax <= c.SoilMoistureMin {
		return c, errors.New("parâmetros preditivos inválidos: confira SOIL_PREDICTION_PROCESS_NOISE e SOIL_MOISTURE_MIN_VALUE/SOIL_MOISTURE_MAX_VALUE")
	}
	if c.TrendStableThreshold < 0 || c.HighUncertaintyWidth <= 0 || c.DiagnosticMinSamples < 2 || c.SoilZeroTolerance < 0 || c.SoilSaturationMargin < 0 {
		return c, errors.New("parâmetros de diagnóstico inválidos: confira SOIL_TREND_STABLE_THRESHOLD, SOIL_PREDICTION_HIGH_UNCERTAINTY_WIDTH, SOIL_DIAGNOSTIC_CONSECUTIVE_SAMPLES, SOIL_ZERO_TOLERANCE e SOIL_SATURATION_MARGIN")
	}
	rangeWidth := c.SoilMoistureMax - c.SoilMoistureMin
	if c.SoilZeroTolerance >= rangeWidth || c.SoilSaturationMargin >= rangeWidth || c.SoilZeroTolerance+c.SoilSaturationMargin >= rangeWidth {
		return c, errors.New("SOIL_ZERO_TOLERANCE e SOIL_SATURATION_MARGIN devem ser menores que a faixa de umidade e não devem se sobrepor")
	}
	if int(math.Ceil(float64(c.PredictionHorizonHours*60)/float64(c.PredictionStepMinutes))) > 500 {
		return c, errors.New("a resolução da previsão gera pontos demais; aumente SOIL_PREDICTION_STEP_MINUTES")
	}
	return c, nil
}

type Sensor struct {
	DevEUI  string `json:"devEUI"`
	Name    string `json:"name"`
	Payload struct {
		AirHumidity    float64  `json:"air_humidity"`
		AirTemperature float64  `json:"air_temperature"`
		SoilMoisture   *float64 `json:"soil_moisture"`
		Timestamp      int64    `json:"timestamp"`
		Validity       bool     `json:"validity"`
	} `json:"payload"`
}

type DeviceLocation struct {
	DevEUI    string  `json:"devEUI"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (d DeviceLocation) coords() (float64, float64) {
	lat, lon := d.Lat, d.Lon
	if lat == 0 && d.Latitude != 0 {
		lat = d.Latitude
	}
	if lon == 0 && d.Longitude != 0 {
		lon = d.Longitude
	}
	return lat, lon
}

func get(ctx context.Context, client *http.Client, rawURL string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1500))
		return fmt.Errorf("GET %s respondeu HTTP %d: %s", req.URL.Host, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func latestSensor(ctx context.Context, c Config, client *http.Client, user, devEUI string) (Sensor, []SensorHistoryPoint, float64, SoilMoisturePrediction, error) {
	target := strings.NewReplacer("{user}", url.PathEscape(user), "{devEUI}", url.PathEscape(devEUI)).Replace(c.SensorURL)
	headers := map[string]string{"Accept": "application/json"}
	if c.SensorToken != "" {
		headers["Authorization"] = "Bearer " + c.SensorToken
	}
	var raw json.RawMessage
	if err := get(ctx, client, target, headers, &raw); err != nil {
		return Sensor{}, nil, 0, SoilMoisturePrediction{}, fmt.Errorf("API de sensores: %w", err)
	}

	var readings []Sensor
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &readings); err != nil {
			return Sensor{}, nil, 0, SoilMoisturePrediction{}, err
		}
	} else {
		var s Sensor
		if err := json.Unmarshal(raw, &s); err != nil {
			return Sensor{}, nil, 0, SoilMoisturePrediction{}, err
		}
		readings = []Sensor{s}
	}

	// GAMBIARRA TEMPORÁRIA:
	// reposiciona os timestamps para simular leituras recentes.
	// Ativada somente quando TEMP_FIX_SENSOR_TIMESTAMP=true.
	if c.FixSensorTimestamp {
		var maxRawTimestamp int64

		// Encontra a leitura válida com o maior timestamp
		// correspondente ao sensor solicitado.
		for _, s := range readings {
			if s.DevEUI != "" && !strings.EqualFold(s.DevEUI, devEUI) {
				continue
			}

			if s.Payload.Timestamp <= 0 ||
				!s.Payload.Validity ||
				s.Payload.SoilMoisture == nil {
				continue
			}

			if s.Payload.Timestamp > maxRawTimestamp {
				maxRawTimestamp = s.Payload.Timestamp
			}
		}

		// Desloca as leituras, preservando o intervalo entre elas.
		if maxRawTimestamp > 0 {
			offset := time.Now().Unix() - maxRawTimestamp

			for i := range readings {
				if readings[i].Payload.Timestamp <= 0 {
					continue
				}

				if readings[i].DevEUI != "" &&
					!strings.EqualFold(readings[i].DevEUI, devEUI) {
					continue
				}

				readings[i].Payload.Timestamp += offset
			}

			log.Printf(
				"[TEMP_FIX_SENSOR_TIMESTAMP] ativo | sensor=%s | timestamp_original=%d | deslocamento=%d segundos",
				devEUI,
				maxRawTimestamp,
				offset,
			)
		}
	}

	// O filtro precisa de leituras repetidas da mesma variável, ordenadas no tempo.
	valid := make([]Sensor, 0, len(readings))
	now := time.Now()
	for _, s := range readings {
		if s.DevEUI != "" && !strings.EqualFold(s.DevEUI, devEUI) {
			continue
		}
		if s.Payload.Timestamp <= 0 || !s.Payload.Validity || s.Payload.SoilMoisture == nil {
			continue
		}
		age := now.Sub(time.Unix(s.Payload.Timestamp, 0))
		if age < -5*time.Minute || age > c.SensorMaxAge {
			continue
		}
		valid = append(valid, s)
	}
	if len(valid) == 0 {
		return Sensor{}, nil, 0, SoilMoisturePrediction{}, errors.New("API não retornou leitura válida e recente para esse sensor")
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Payload.Timestamp < valid[j].Payload.Timestamp })
	values := make([]float64, len(valid))
	timestamps := make([]int64, len(valid))
	for i, s := range valid {
		values[i] = *s.Payload.SoilMoisture
		timestamps[i] = s.Payload.Timestamp
	}

	// A série filtrada e o modelo preditivo são calculados no backend.
	filtered := kalman(values, c.KalmanQ, c.KalmanR)
	history := make([]SensorHistoryPoint, 0, len(valid))
	for i, s := range valid {
		history = append(history, SensorHistoryPoint{
			Timestamp:                  time.Unix(s.Payload.Timestamp, 0).UTC(),
			SoilMoistureRaw:            values[i],
			SoilMoistureKalmanFiltered: filtered[i],
		})
	}

	latestIndex := len(valid) - 1
	filteredSoil := filtered[latestIndex]
	trendState := kalmanLevelTrend(values, timestamps, c.KalmanR, c.PredictionProcessNoise)
	prediction := predictSoilMoisture(
		trendState,
		filteredSoil,
		time.Unix(timestamps[latestIndex], 0).UTC(),
		c,
	)
	enrichPrediction(&prediction, values, trendState.slope, c)

	return valid[latestIndex], history, filteredSoil, prediction, nil
}

func sensorLocation(
    supabaseClient *supabase.Client,
    c Config,
    user string,
    devEUI string,
) (float64, float64, error) {
    user = strings.TrimSpace(user)
    devEUI = strings.TrimSpace(devEUI)

    if user == "" || devEUI == "" {
        return 0, 0, errors.New("userId e devEUI são obrigatórios")
    }

    var rows []map[string]json.RawMessage

    _, err := supabaseClient.
		From(c.SupabaseTable).
		Select(c.DevicesColumn, "exact", false).
		Filter(c.UserColumn, "eq", user).
		ExecuteTo(&rows)

    if err != nil {
        return 0, 0, fmt.Errorf(
            "Supabase: falha na consulta da tabela %s usando %s=%q: %w",
            c.SupabaseTable,
            c.UserColumn,
            user,
            err,
        )
    }

    if len(rows) == 0 {
        return 0, 0, fmt.Errorf(
            "Supabase: nenhum registro visível na tabela %s para %s=%q; confira projeto, chave, permissões e RLS",
            c.SupabaseTable,
            c.UserColumn,
            user,
        )
    }

    devices, exists := rows[0][c.DevicesColumn]
    if !exists || len(devices) == 0 || string(devices) == "null" {
        return 0, 0, fmt.Errorf(
            "Supabase: o usuário foi encontrado, mas a coluna %s está ausente ou nula",
            c.DevicesColumn,
        )
    }

    // Estrutura habitual: {"1": {"devEUI": "...", "lat": ..., "lon": ...}}
    var object map[string]json.RawMessage
    if err := json.Unmarshal(devices, &object); err == nil {
        // Também aceita dispositivos indexados diretamente pelo devEUI.
        if direct, ok := object[devEUI]; ok {
            var d DeviceLocation
            if err := json.Unmarshal(direct, &d); err == nil {
                return checkedCoords(d)
            }
        }

        for _, raw := range object {
            var d DeviceLocation
            if err := json.Unmarshal(raw, &d); err != nil {
                continue
            }

            if strings.EqualFold(strings.TrimSpace(d.DevEUI), devEUI) {
                return checkedCoords(d)
            }
        }
    }

    // Também aceita uma lista JSON de dispositivos.
    var devicesArray []DeviceLocation
    if err := json.Unmarshal(devices, &devicesArray); err == nil {
        for _, d := range devicesArray {
            if strings.EqualFold(strings.TrimSpace(d.DevEUI), devEUI) {
                return checkedCoords(d)
            }
        }
    }

    return 0, 0, fmt.Errorf(
        "Supabase: dispositivo %s não encontrado na coluna %s do usuário %s",
        devEUI,
        c.DevicesColumn,
        user,
    )
}

func checkedCoords(d DeviceLocation) (float64, float64, error) {
	lat, lon := d.coords()
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
		return 0, 0, errors.New("latitude/longitude inválidas no Supabase")
	}
	return lat, lon, nil
}

type Weather struct {
	Timezone string `json:"timezone"`
	Daily    struct {
		Time            []string  `json:"time"`
		RainMM          []float64 `json:"precipitation_sum"`
		RainProbability []float64 `json:"precipitation_probability_max"`
		ET0             []float64 `json:"et0_fao_evapotranspiration"`
	} `json:"daily"`
}

type ForecastDay struct {
	Date        string  `json:"date"`
	Probability float64 `json:"precipitation_probability_percent"`
	RainMM      float64 `json:"precipitation_mm"`
	ET0MM       float64 `json:"et0_mm,omitempty"`
	RainLikely  bool    `json:"rain_likely"`
}

// SensorHistoryPoint é calculado no MSS para o frontend apenas renderizar as séries.
type SensorHistoryPoint struct {
	Timestamp                  time.Time `json:"timestamp"`
	SoilMoistureRaw            float64   `json:"soil_moisture_raw"`
	SoilMoistureKalmanFiltered float64   `json:"soil_moisture_kalman_filtered"`
}

type SoilMoisturePredictionPoint struct {
	Timestamp             time.Time `json:"timestamp"`
	HoursAhead            float64   `json:"hours_ahead"`
	SoilMoisturePredicted float64   `json:"soil_moisture_predicted"`
	LowerBound            float64   `json:"lower_bound"`
	UpperBound            float64   `json:"upper_bound"`
}

type SoilMoistureDiagnostics struct {
	PersistentZero             bool     `json:"persistent_zero"`
	SensorSaturated            bool     `json:"sensor_saturated"`
	ConsecutiveNearZero        int      `json:"consecutive_near_zero"`
	ConsecutiveNearMaximum     int      `json:"consecutive_near_maximum"`
	RequiredConsecutiveSamples int      `json:"required_consecutive_samples"`
	Messages                   []string `json:"messages"`
}

type SoilMoisturePrediction struct {
	Model                   string                        `json:"model"`
	Unit                    string                        `json:"unit"`
	BaseTimestamp           time.Time                     `json:"base_timestamp"`
	HorizonHours            int                           `json:"horizon_hours"`
	StepMinutes             int                           `json:"step_minutes"`
	CurrentFiltered         float64                       `json:"current_filtered"`
	TrendPerHour            float64                       `json:"trend_per_hour"`
	TrendStatus             string                        `json:"trend_status"`
	PredictedAtHorizon      float64                       `json:"predicted_at_horizon"`
	ChangeFromCurrent       float64                       `json:"change_from_current"`
	PredictionIntervalWidth float64                       `json:"prediction_interval_width"`
	ConfidenceStatus        string                        `json:"confidence_status"`
	Diagnostics             SoilMoistureDiagnostics       `json:"diagnostics"`
	Points                  []SoilMoisturePredictionPoint `json:"points"`
}

type ForecastSummary struct {
	Days                        int      `json:"days"`
	TotalPrecipitationMM        float64  `json:"total_precipitation_mm"`
	MaxPrecipitationProbability float64  `json:"max_precipitation_probability_percent"`
	DaysWithLikelyRain          int      `json:"days_with_likely_rain"`
	ET0Available                bool     `json:"et0_available"`
	TotalET0MM                  *float64 `json:"total_et0_mm,omitempty"`
}

type levelTrendState struct {
	level float64
	slope float64 // unidades do sensor por hora
	p00   float64
	p01   float64
	p11   float64
}

func weatherForecast(ctx context.Context, client *http.Client, lat, lon float64, timezone string) (Weather, error) {
	u, _ := url.Parse(weatherURL)
	q := u.Query()
	q.Set("latitude", strconv.FormatFloat(lat, 'f', 6, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', 6, 64))
	q.Set("timezone", timezone)
	q.Set("forecast_days", "6") // hoje + próximos cinco dias
	q.Set("daily", "precipitation_sum,precipitation_probability_max,et0_fao_evapotranspiration")
	u.RawQuery = q.Encode()
	var w Weather
	if err := get(ctx, client, u.String(), map[string]string{"Accept": "application/json"}, &w); err != nil {
		return w, fmt.Errorf("Open-Meteo: %w", err)
	}
	if len(w.Daily.Time) < 6 || len(w.Daily.RainMM) < 6 || len(w.Daily.RainProbability) < 6 {
		return w, errors.New("previsão meteorológica incompleta para seis dias")
	}
	return w, nil
}

// kalman suaviza leituras sequenciais da mesma variável (umidade do solo).
func kalman(values []float64, q, r float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	filtered := make([]float64, len(values))
	x, covariance := values[0], r
	filtered[0] = x
	for i := 1; i < len(values); i++ {
		covariance += q
		gain := covariance / (covariance + r)
		x += gain * (values[i] - x)
		covariance *= 1 - gain
		filtered[i] = x
	}
	return filtered
}

// kalmanLevelTrend estima simultaneamente o nível da umidade e sua tendência.
// O tempo é expresso em horas e a tendência em unidades do sensor por hora.
// O ruído de processo é modelado como aceleração branca para intervalos irregulares.
func kalmanLevelTrend(values []float64, timestamps []int64, measurementNoise, processNoise float64) levelTrendState {
	if len(values) == 0 {
		return levelTrendState{}
	}

	state := levelTrendState{
		level: values[0],
		slope: 0,
		p00:   measurementNoise,
		p01:   0,
		p11:   25, // incerteza inicial da tendência: (unidades do sensor/hora)^2
	}

	for i := 1; i < len(values); i++ {
		dt := float64(timestamps[i]-timestamps[i-1]) / 3600.0
		if dt <= 0 {
			dt = 1.0 / 3600.0 // evita uma transição temporal nula
		}

		// Predição do estado: x = [nível, tendência].
		predLevel := state.level + dt*state.slope
		predSlope := state.slope

		// P_pred = F P F' + Q, com F = [[1, dt], [0, 1]].
		p00 := state.p00 + dt*(state.p01+state.p01) + dt*dt*state.p11 + processNoise*dt*dt*dt/3.0
		p01 := state.p01 + dt*state.p11 + processNoise*dt*dt/2.0
		p11 := state.p11 + processNoise*dt

		innovationVariance := p00 + measurementNoise
		if innovationVariance <= 0 || math.IsNaN(innovationVariance) || math.IsInf(innovationVariance, 0) {
			continue
		}
		k0 := p00 / innovationVariance
		k1 := p01 / innovationVariance
		innovation := values[i] - predLevel

		state.level = predLevel + k0*innovation
		state.slope = predSlope + k1*innovation
		state.p00 = math.Max(0, p00-k0*p00)
		state.p01 = p01 - k0*p01
		state.p11 = math.Max(0, p11-k1*p01)
	}

	return state
}

// predictSoilMoisture gera uma projeção futura no backend. A curva parte do último
// valor filtrado já publicado e usa a tendência estimada pelo Kalman de nível+tendência.
// Os limites de incerteza são aproximados (95%, normal) e limitados à escala configurada.
func predictSoilMoisture(state levelTrendState, currentFiltered float64, base time.Time, c Config) SoilMoisturePrediction {
	prediction := SoilMoisturePrediction{
		Model:           "kalman_level_trend",
		Unit:            "sensor_scale",
		BaseTimestamp:   base.UTC(),
		HorizonHours:    c.PredictionHorizonHours,
		StepMinutes:     c.PredictionStepMinutes,
		CurrentFiltered: clamp(currentFiltered, c.SoilMoistureMin, c.SoilMoistureMax),
		TrendPerHour:    state.slope,
		Points:          make([]SoilMoisturePredictionPoint, 0, int(math.Ceil(float64(c.PredictionHorizonHours*60)/float64(c.PredictionStepMinutes)))+1),
	}

	horizonMinutes := c.PredictionHorizonHours * 60
	for elapsedMinutes := 0; elapsedMinutes <= horizonMinutes; {
		hours := float64(elapsedMinutes) / 60.0
		value := prediction.CurrentFiltered + state.slope*hours

		// A variância cresce com o horizonte e com o ruído de processo.
		variance := state.p00 + 2*hours*state.p01 + hours*hours*state.p11 + c.PredictionProcessNoise*hours*hours*hours/3.0
		if variance < 0 || math.IsNaN(variance) || math.IsInf(variance, 0) {
			variance = 0
		}
		sigma := math.Sqrt(variance)
		lower := value - 1.96*sigma
		upper := value + 1.96*sigma

		value = clamp(value, c.SoilMoistureMin, c.SoilMoistureMax)
		lower = clamp(lower, c.SoilMoistureMin, c.SoilMoistureMax)
		upper = clamp(upper, c.SoilMoistureMin, c.SoilMoistureMax)
		if lower > upper {
			lower, upper = upper, lower
		}

		prediction.Points = append(prediction.Points, SoilMoisturePredictionPoint{
			Timestamp:             base.UTC().Add(time.Duration(elapsedMinutes) * time.Minute),
			HoursAhead:            hours,
			SoilMoisturePredicted: value,
			LowerBound:            lower,
			UpperBound:            upper,
		})

		if elapsedMinutes == horizonMinutes {
			break
		}
		next := elapsedMinutes + c.PredictionStepMinutes
		if next > horizonMinutes {
			elapsedMinutes = horizonMinutes
		} else {
			elapsedMinutes = next
		}
	}

	if len(prediction.Points) > 0 {
		prediction.PredictedAtHorizon = prediction.Points[len(prediction.Points)-1].SoilMoisturePredicted
		prediction.ChangeFromCurrent = prediction.PredictedAtHorizon - prediction.CurrentFiltered
	}
	return prediction
}

func clamp(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func classifyTrend(slopePerHour, stableThreshold float64) string {
	switch {
	case slopePerHour > stableThreshold:
		return "increasing"
	case slopePerHour < -stableThreshold:
		return "decreasing"
	default:
		return "stable"
	}
}

func trailingCount(values []float64, predicate func(float64) bool) int {
	count := 0
	for i := len(values) - 1; i >= 0; i-- {
		if !predicate(values[i]) {
			break
		}
		count++
	}
	return count
}

// diagnoseSoilMoisture classifica padrões de leituras-limite. As condições são
// calculadas no backend e servem para evitar recomendações automáticas quando
// há indícios de leitura presa no limite inferior/superior do sensor.
func diagnoseSoilMoisture(values []float64, c Config) SoilMoistureDiagnostics {
	zeroLimit := c.SoilMoistureMin + c.SoilZeroTolerance
	maxLimit := c.SoilMoistureMax - c.SoilSaturationMargin
	nearZero := trailingCount(values, func(v float64) bool { return v <= zeroLimit })
	nearMaximum := trailingCount(values, func(v float64) bool { return v >= maxLimit })

	d := SoilMoistureDiagnostics{
		PersistentZero:             nearZero >= c.DiagnosticMinSamples,
		SensorSaturated:            nearMaximum >= c.DiagnosticMinSamples,
		ConsecutiveNearZero:        nearZero,
		ConsecutiveNearMaximum:     nearMaximum,
		RequiredConsecutiveSamples: c.DiagnosticMinSamples,
		Messages:                   make([]string, 0, 2),
	}
	if d.PersistentZero {
		d.Messages = append(d.Messages, fmt.Sprintf("As últimas %d ou mais leituras ficaram próximas do limite inferior configurado; verifique a calibração, a instalação e a escala do sensor antes de confiar em uma recomendação automática.", c.DiagnosticMinSamples))
	}
	if d.SensorSaturated {
		d.Messages = append(d.Messages, fmt.Sprintf("As últimas %d ou mais leituras ficaram próximas do limite superior configurado; verifique possível saturação e calibração do sensor.", c.DiagnosticMinSamples))
	}
	return d
}

func enrichPrediction(prediction *SoilMoisturePrediction, values []float64, trendPerHour float64, c Config) {
	prediction.TrendStatus = classifyTrend(trendPerHour, c.TrendStableThreshold)
	prediction.Diagnostics = diagnoseSoilMoisture(values, c)
	if len(prediction.Points) == 0 {
		prediction.ConfidenceStatus = "unavailable"
		prediction.PredictionIntervalWidth = 0
		return
	}
	last := prediction.Points[len(prediction.Points)-1]
	width := math.Max(0, last.UpperBound-last.LowerBound)
	prediction.PredictionIntervalWidth = width
	if width >= c.HighUncertaintyWidth {
		prediction.ConfidenceStatus = "high_uncertainty"
		prediction.Diagnostics.Messages = append(prediction.Diagnostics.Messages, "A faixa de incerteza no fim do horizonte excede o limite configurado; interprete a projeção com cautela.")
	} else {
		prediction.ConfidenceStatus = "within_threshold"
	}
	if len(prediction.Diagnostics.Messages) == 0 {
		prediction.Diagnostics.Messages = append(prediction.Diagnostics.Messages, "Nenhuma condição de diagnóstico foi acionada pelos limites configurados.")
	}
}

func summarizeForecast(days []ForecastDay, et0Available bool) ForecastSummary {
	summary := ForecastSummary{Days: len(days), ET0Available: et0Available}
	var totalET0 float64
	for _, day := range days {
		summary.TotalPrecipitationMM += day.RainMM
		if day.Probability > summary.MaxPrecipitationProbability {
			summary.MaxPrecipitationProbability = day.Probability
		}
		if day.RainLikely {
			summary.DaysWithLikelyRain++
		}
		totalET0 += day.ET0MM
	}
	if et0Available {
		summary.TotalET0MM = &totalET0
	}
	return summary
}

func buildIrrigationRecommendation(soil float64, days []ForecastDay, now time.Time, loc *time.Location, c Config, diagnostics SoilMoistureDiagnostics) map[string]any {
	soilStatus, action := "intermediate", "manual_check"
	var shouldIrrigate any
	if soil <= c.Dry {
		soilStatus, action, shouldIrrigate = "dry", "irrigation_needed", true
	}
	if soil >= c.Wet {
		soilStatus, action, shouldIrrigate = "wet", "not_needed_now", false
	}

	result := map[string]any{
		"soil_status":                soilStatus,
		"action":                     action,
		"should_irrigate":            shouldIrrigate,
		"should_irrigate_today":      nil,
		"recommended_day":            nil,
		"requires_manual_validation": false,
	}

	// Leituras repetidas nos limites físicos podem representar condição real,
	// mas também falha de instalação/calibração. Não emitimos decisão automática.
	if diagnostics.PersistentZero || diagnostics.SensorSaturated {
		result["action"] = "manual_check_sensor"
		result["should_irrigate"] = nil
		result["should_irrigate_today"] = nil
		result["recommended_day"] = nil
		result["requires_manual_validation"] = true
		if diagnostics.PersistentZero && diagnostics.SensorSaturated {
			result["message"] = "As leituras recentes oscilaram entre os limites configurados do sensor. Verifique o sensor e a calibração antes de decidir sobre irrigação."
		} else if diagnostics.PersistentZero {
			result["message"] = "As leituras recentes permaneceram próximas do limite inferior do sensor. Verifique a calibração e a instalação antes de decidir sobre irrigação."
		} else {
			result["message"] = "As leituras recentes permaneceram próximas do limite superior do sensor. Verifique possível saturação e a calibração antes de decidir sobre irrigação."
		}
		return result
	}

	if soilStatus == "wet" {
		result["should_irrigate_today"] = false
		result["message"] = "O sensor indica solo úmido; não irrigue agora. Reavalie após novas leituras."
		return result
	}
	if soilStatus == "intermediate" {
		result["requires_manual_validation"] = true
		result["message"] = "A leitura está entre os limiares; calibre o sensor e verifique a cultura antes de decidir."
		return result
	}
	if len(days) == 0 {
		result["action"] = "manual_check"
		result["should_irrigate"] = nil
		result["message"] = "Não há dias de previsão disponíveis para selecionar uma data segura. Verifique as condições manualmente."
		return result
	}

	best, bestScore := 0, math.Inf(1)
	for i, day := range days {
		score := day.Probability + math.Min(day.RainMM*3, 30)
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	day := days[best]
	recommendedToday := day.Date == now.In(loc).Format("2006-01-02")
	result["recommended_day"] = day.Date
	result["should_irrigate_today"] = recommendedToday
	result["rain_probability_percent"] = day.Probability
	result["expected_rain_mm"] = day.RainMM
	if day.Probability >= c.RainProbability && day.RainMM >= c.MinRainMM {
		result["message"] = "O solo está seco. Este é o dia de menor risco na janela, mas ainda há previsão relevante de chuva; confira novamente o solo e a previsão antes de irrigar."
		result["action"] = "irrigate_with_caution"
	} else {
		result["message"] = "O solo está na faixa seca. Este é o dia com menor risco combinado de chuva na previsão; confirme a previsão novamente antes de irrigar."
		result["action"] = "irrigate_on_recommended_day"
	}
	return result
}

type Response struct {
	SensorID               string                 `json:"sensor_id"`
	Location               map[string]any         `json:"location"`
	Sensor                 map[string]any         `json:"sensor"`
	SensorHistory          []SensorHistoryPoint   `json:"sensor_history"`
	SoilMoisturePrediction SoilMoisturePrediction `json:"soil_moisture_prediction"`
	Forecast               []ForecastDay          `json:"forecast_today_and_next_5_days"`
	ForecastSummary        ForecastSummary        `json:"forecast_summary"`
	Irrigation             map[string]any         `json:"irrigation"`
	Note                   string                 `json:"note"`
}

func handler(c Config, client *http.Client, supabaseClient *supabase.Client,) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, devEUI := r.URL.Query().Get("userId"), r.URL.Query().Get("devEUI")
		if user == "" || devEUI == "" {
			respond(w, 400, map[string]string{"error": "informe userId e devEUI na query string"})
			return
		}
		tz := env("IRRIGATION_TIMEZONE", "America/Sao_Paulo")
		if value := r.URL.Query().Get("timezone"); value != "" {
			tz = value
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			respond(w, 400, map[string]string{"error": "timezone inválido; use, por exemplo, America/Sao_Paulo"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), c.Timeout)
		defer cancel()

		sensor, sensorHistory, filteredSoil, soilPrediction, err := latestSensor(ctx, c, client, user, devEUI)
		if err != nil {
			respond(w, 502, map[string]string{"error": err.Error()})
			return
		}
		lat, lon, err := sensorLocation(supabaseClient, c, user, devEUI)
		if err != nil {
			respond(w, 502, map[string]string{"error": err.Error()})
			return
		}
		forecast, err := weatherForecast(ctx, client, lat, lon, tz)
		if err != nil {
			respond(w, 502, map[string]string{"error": err.Error()})
			return
		}

		days := make([]ForecastDay, 0, 6)
		for i, date := range forecast.Daily.Time[:6] {
			mm, p := forecast.Daily.RainMM[i], forecast.Daily.RainProbability[i]
			et0 := 0.0
			if i < len(forecast.Daily.ET0) {
				et0 = forecast.Daily.ET0[i]
			}
			days = append(days, ForecastDay{Date: date, Probability: p, RainMM: mm, ET0MM: et0, RainLikely: p >= c.RainProbability})
		}

		forecastSummary := summarizeForecast(days, len(forecast.Daily.ET0) >= len(days))
		irrigation := buildIrrigationRecommendation(filteredSoil, days, time.Now(), loc, c, soilPrediction.Diagnostics)

		respond(w, 200, Response{
			SensorID: devEUI,
			Location: map[string]any{"latitude": lat, "longitude": lon, "timezone": tz, "source": "Supabase"},
			Sensor: map[string]any{
				"timestamp":                     time.Unix(sensor.Payload.Timestamp, 0).UTC(),
				"soil_moisture_raw":             *sensor.Payload.SoilMoisture,
				"soil_moisture_kalman_filtered": filteredSoil,
				"air_humidity_percent":          sensor.Payload.AirHumidity,
				"air_temperature_celsius":       sensor.Payload.AirTemperature,
				"validity":                      sensor.Payload.Validity,
			},
			SensorHistory:          sensorHistory,
			SoilMoisturePrediction: soilPrediction,
			Forecast:               days,
			ForecastSummary:        forecastSummary,
			Irrigation:             irrigation,
			Note:                   "O MSS calcula as séries históricas, a projeção, os diagnósticos e a recomendação. trend_status, confidence_status e diagnostics são determinados no backend. Leituras repetidas próximas aos limites configurados exigem validação manual. A projeção é estatística e não incorpora chuva prevista, ET₀, irrigação aplicada, drenagem ou características do solo; valide e calibre com dados de campo antes do uso operacional.",
		})
	}
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
    c, err := config()
    if err != nil {
        log.Fatal(err)
    }

    supabaseClient, err := supabase.NewClient(
        c.SupabaseURL,
        c.SupabaseKey,
        nil,
    )
    if err != nil {
        log.Fatalf("Falha ao inicializar cliente Supabase: %v", err)
    }

    log.Println("Cliente Supabase inicializado com sucesso")

    client := &http.Client{Timeout: c.Timeout}

    mux := http.NewServeMux()
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        respond(w, 200, map[string]string{"status": "ok"})
    })

    mux.Handle(
        "GET /api/v1/irrigation",
        handler(c, client, supabaseClient),
    )

    server := &http.Server{
        Addr:              ":" + c.Port,
        Handler:           mux,
        ReadHeaderTimeout: 5 * time.Second,
    }

    log.Printf("irrigacao-mss ouvindo na porta %s", c.Port)
    log.Fatal(server.ListenAndServe())
}