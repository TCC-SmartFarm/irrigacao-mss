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
)

const weatherURL = "https://api.open-meteo.com/v1/forecast"

type Config struct {
	Port, SensorURL, SensorToken             string
	SupabaseURL, SupabaseKey string

	FixSensorTimestamp bool

	SupabaseTable, UserColumn, DevicesColumn string
	Dry, Wet, RainProbability, MinRainMM     float64
	KalmanQ, KalmanR                         float64
	Timeout, SensorMaxAge                    time.Duration
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

func config() (Config, error) {
	c := Config{
		FixSensorTimestamp: strings.EqualFold(
			env("TEMP_FIX_SENSOR_TIMESTAMP", "false"),
			"true",
		),
		Port: env("PORT", "8456"), 
		SensorURL: strings.TrimSpace(os.Getenv("SENSOR_API_URL_TEMPLATE")),
		SensorToken: os.Getenv("SENSOR_API_TOKEN"), 
		SupabaseURL: strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseKey: os.Getenv("SUPABASE_KEY"), 
		SupabaseTable: env("SUPABASE_TABLE", "profiles"), 
		UserColumn: env("SUPABASE_USER_COLUMN", "id"),
		DevicesColumn: env("SUPABASE_DEVICES_COLUMN", "devices"),
		Dry:           number("SOIL_DRY_THRESHOLD", 30), 
		Wet: number("SOIL_WET_THRESHOLD", 70),
		RainProbability: number("RAIN_PROBABILITY_THRESHOLD", 60), 
		MinRainMM: number("MIN_RAIN_MM", 3),
		KalmanQ: number("KALMAN_PROCESS_NOISE", 1), 
		KalmanR: number("KALMAN_MEASUREMENT_NOISE", 4),
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

func latestSensor(ctx context.Context, c Config, client *http.Client, user, devEUI string) (Sensor, float64, error) {
	target := strings.NewReplacer("{user}", url.PathEscape(user), "{devEUI}", url.PathEscape(devEUI)).Replace(c.SensorURL)
	headers := map[string]string{"Accept": "application/json"}
	if c.SensorToken != "" {
		headers["Authorization"] = "Bearer " + c.SensorToken
	}
	var raw json.RawMessage
	if err := get(ctx, client, target, headers, &raw); err != nil {
		return Sensor{}, 0, fmt.Errorf("API de sensores: %w", err)
	}

	var readings []Sensor
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &readings); err != nil {
			return Sensor{}, 0, err
		}
	} else {
		var s Sensor
		if err := json.Unmarshal(raw, &s); err != nil {
			return Sensor{}, 0, err
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
		return Sensor{}, 0, errors.New("API não retornou leitura válida e recente para esse sensor")
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Payload.Timestamp < valid[j].Payload.Timestamp })
	values := make([]float64, len(valid))
	for i, s := range valid {
		values[i] = *s.Payload.SoilMoisture
	}
	filtered := kalman(values, c.KalmanQ, c.KalmanR)
	return valid[len(valid)-1], filtered[len(filtered)-1], nil
}

func sensorLocation(ctx context.Context, c Config, client *http.Client, user, devEUI string) (float64, float64, error) {
	base, err := url.Parse(c.SupabaseURL + "/rest/v1/" + url.PathEscape(c.SupabaseTable))
	if err != nil {
		return 0, 0, err
	}
	q := base.Query()
	q.Set("select", c.DevicesColumn)
	q.Set(c.UserColumn, "eq."+user)
	q.Set("limit", "1")
	base.RawQuery = q.Encode()
	headers := map[string]string{
		"apikey":  c.SupabaseKey,
		"Accept": "application/json",
	}
	var rows []map[string]json.RawMessage
	if err := get(ctx, client, base.String(), headers, &rows); err != nil {
		return 0, 0, fmt.Errorf("Supabase: %w", err)
	}
	if len(rows) == 0 {
		return 0, 0, errors.New("nenhum registro encontrado no Supabase para esse usuário")
	}
	devices := rows[0][c.DevicesColumn]
	var object map[string]json.RawMessage
	if err := json.Unmarshal(devices, &object); err == nil {
		// A coluna devices pode ser um objeto JSON indexado por "1", "2" etc.
		if direct, ok := object[devEUI]; ok {
			var d DeviceLocation
			if json.Unmarshal(direct, &d) == nil {
				return checkedCoords(d)
			}
		}
		for _, raw := range object {
			var d DeviceLocation
			if json.Unmarshal(raw, &d) == nil && strings.EqualFold(d.DevEUI, devEUI) {
				return checkedCoords(d)
			}
		}
	}
	var array []DeviceLocation
	if err := json.Unmarshal(devices, &array); err == nil {
		for _, d := range array {
			if strings.EqualFold(d.DevEUI, devEUI) {
				return checkedCoords(d)
			}
		}
	}
	return 0, 0, fmt.Errorf("devEUI %s não foi encontrado na coluna %s do Supabase", devEUI, c.DevicesColumn)
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

type Response struct {
	SensorID   string         `json:"sensor_id"`
	Location   map[string]any `json:"location"`
	Sensor     map[string]any `json:"sensor"`
	Forecast   []ForecastDay  `json:"forecast_today_and_next_5_days"`
	Irrigation map[string]any `json:"irrigation"`
	Note       string         `json:"note"`
}

func handler(c Config, client *http.Client) http.HandlerFunc {
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

		sensor, filteredSoil, err := latestSensor(ctx, c, client, user, devEUI)
		if err != nil {
			respond(w, 502, map[string]string{"error": err.Error()})
			return
		}
		lat, lon, err := sensorLocation(ctx, c, client, user, devEUI)
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

		soil := filteredSoil
		soilStatus, needed := "intermediate", "manual_check"
		shouldIrrigate := any(nil)
		if soil <= c.Dry {
			soilStatus, needed, shouldIrrigate = "dry", "irrigation_needed", true
		}
		if soil >= c.Wet {
			soilStatus, needed, shouldIrrigate = "wet", "not_needed_now", false
		}

		irrigation := map[string]any{"soil_status": soilStatus, "action": needed, "should_irrigate": shouldIrrigate, "should_irrigate_today": nil, "recommended_day": nil}
		if soilStatus == "wet" {
			irrigation["should_irrigate_today"] = false
			irrigation["message"] = "O sensor indica solo úmido; não irrigue agora. Reavalie após novas leituras."
		} else if soilStatus == "intermediate" {
			irrigation["message"] = "A leitura está entre os limiares; calibre o sensor e verifique a cultura antes de decidir."
		} else {
			best := 0
			bestScore := math.Inf(1)
			for i, day := range days {
				// Pontuação simples: menor probabilidade prevista e menor volume previsto.
				score := day.Probability + math.Min(day.RainMM*3, 30)
				if score < bestScore {
					best, bestScore = i, score
				}
			}
			d := days[best]
			irrigation["recommended_day"] = d.Date
			irrigation["should_irrigate_today"] = d.Date == time.Now().In(loc).Format("2006-01-02")
			irrigation["rain_probability_percent"] = d.Probability
			irrigation["expected_rain_mm"] = d.RainMM
			if d.Probability >= c.RainProbability && d.RainMM >= c.MinRainMM {
				irrigation["message"] = "O solo está seco. Este é o dia de menor risco na janela, mas ainda há previsão relevante de chuva; confira novamente o solo e a previsão antes de irrigar."
				irrigation["action"] = "irrigate_with_caution"
			} else {
				irrigation["message"] = "O solo está na faixa seca. Este é o dia com menor risco combinado de chuva na previsão; confirme a previsão novamente antes de irrigar."
				irrigation["action"] = "irrigate_on_recommended_day"
			}
		}

		respond(w, 200, Response{
			SensorID: devEUI,
			Location: map[string]any{"latitude": lat, "longitude": lon, "timezone": tz, "source": "Supabase"},
			Sensor:   map[string]any{"timestamp": time.Unix(sensor.Payload.Timestamp, 0).UTC(), "soil_moisture_raw": *sensor.Payload.SoilMoisture, "soil_moisture_kalman_filtered": filteredSoil, "air_humidity_percent": sensor.Payload.AirHumidity, "air_temperature_celsius": sensor.Payload.AirTemperature, "validity": sensor.Payload.Validity},
			Forecast: days, Irrigation: irrigation,
			Note: "A probabilidade de chuva vem da Open-Meteo. O Kalman suaviza leituras repetidas de umidade do solo; ele não calcula previsão meteorológica. Calibre os limiares do solo antes de usar operacionalmente.",
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
	client := &http.Client{Timeout: c.Timeout}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	mux.Handle("GET /api/v1/irrigation", handler(c, client))
	server := &http.Server{Addr: ":" + c.Port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("irrigacao-mss ouvindo na porta %s", c.Port)
	log.Fatal(server.ListenAndServe())
}
