package main

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		Dry: 30, Wet: 70, RainProbability: 60, MinRainMM: 3,
		KalmanQ: 1, KalmanR: 4,
		PredictionHorizonHours: 24, PredictionStepMinutes: 60,
		PredictionProcessNoise: 0.05, SoilMoistureMin: 0, SoilMoistureMax: 100,
		TrendStableThreshold: 0.1, HighUncertaintyWidth: 30,
		DiagnosticMinSamples: 5, SoilZeroTolerance: 0.5,
		SoilSaturationMargin: 1,
	}
}

func TestClassifyTrend(t *testing.T) {
	cases := []struct {
		name  string
		slope float64
		want  string
	}{
		{"subindo", 0.5, "increasing"},
		{"caindo", -0.5, "decreasing"},
		{"estavel dentro do limiar", 0.05, "stable"},
		{"limiar inferior", -0.1, "stable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTrend(tc.slope, 0.1); got != tc.want {
				t.Fatalf("classifyTrend(%v) = %q; want %q", tc.slope, got, tc.want)
			}
		})
	}
}

func TestDiagnosePersistentZeroAndSaturation(t *testing.T) {
	c := testConfig()
	zero := diagnoseSoilMoisture([]float64{10, 0, 0, 0, 0, 0}, c)
	if !zero.PersistentZero || zero.SensorSaturated {
		t.Fatalf("diagnóstico de zero inesperado: %+v", zero)
	}
	if zero.ConsecutiveNearZero != 5 {
		t.Fatalf("contagem de zeros consecutivos = %d; want 5", zero.ConsecutiveNearZero)
	}

	saturated := diagnoseSoilMoisture([]float64{85, 100, 100, 100, 100, 100}, c)
	if !saturated.SensorSaturated || saturated.PersistentZero {
		t.Fatalf("diagnóstico de saturação inesperado: %+v", saturated)
	}
	if saturated.ConsecutiveNearMaximum != 5 {
		t.Fatalf("contagem de máximos consecutivos = %d; want 5", saturated.ConsecutiveNearMaximum)
	}
}

func TestDiagnosticsRequireConsecutiveReadings(t *testing.T) {
	c := testConfig()
	d := diagnoseSoilMoisture([]float64{0, 0, 0, 30, 0, 0, 0, 0}, c)
	if d.PersistentZero {
		t.Fatalf("não deveria considerar persistente com apenas quatro amostras finais: %+v", d)
	}
}

func TestPredictionDiagnosticsAndBounds(t *testing.T) {
	c := testConfig()
	base := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	state := levelTrendState{level: 50, slope: -0.2, p00: 4, p01: 0, p11: 25}
	prediction := predictSoilMoisture(state, 50, base, c)
	enrichPrediction(&prediction, []float64{55, 53, 51, 50, 50}, state.slope, c)
	if prediction.TrendStatus != "decreasing" {
		t.Fatalf("trend_status = %q; want decreasing", prediction.TrendStatus)
	}
	if prediction.ConfidenceStatus != "high_uncertainty" {
		t.Fatalf("confidence_status = %q; want high_uncertainty", prediction.ConfidenceStatus)
	}
	if prediction.PredictionIntervalWidth <= 0 {
		t.Fatalf("prediction interval width should be positive: %v", prediction.PredictionIntervalWidth)
	}
	for _, point := range prediction.Points {
		if point.SoilMoisturePredicted < c.SoilMoistureMin || point.SoilMoisturePredicted > c.SoilMoistureMax {
			t.Fatalf("prediction outside configured range: %+v", point)
		}
		if point.LowerBound > point.UpperBound {
			t.Fatalf("invalid interval: %+v", point)
		}
	}
}

func TestForecastSummary(t *testing.T) {
	days := []ForecastDay{
		{Date: "2026-10-22", Probability: 80, RainMM: 4.5, ET0MM: 2, RainLikely: true},
		{Date: "2026-10-23", Probability: 40, RainMM: 1.5, ET0MM: 3, RainLikely: false},
	}
	summary := summarizeForecast(days, true)
	if summary.Days != 2 || math.Abs(summary.TotalPrecipitationMM-6) > 1e-9 {
		t.Fatalf("summary de precipitação inesperado: %+v", summary)
	}
	if summary.MaxPrecipitationProbability != 80 || summary.DaysWithLikelyRain != 1 {
		t.Fatalf("summary meteorológico inesperado: %+v", summary)
	}
	if !summary.ET0Available || summary.TotalET0MM == nil || math.Abs(*summary.TotalET0MM-5) > 1e-9 {
		t.Fatalf("summary de ET0 inesperado: %+v", summary)
	}
}

func TestPersistentBoundaryReadingsRequireManualValidation(t *testing.T) {
	c := testConfig()
	loc := time.UTC
	days := []ForecastDay{{Date: "2026-10-22", Probability: 10, RainMM: 0}}
	diagnostics := diagnoseSoilMoisture([]float64{0, 0, 0, 0, 0, 0}, c)
	result := buildIrrigationRecommendation(0, days, time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), loc, c, diagnostics)
	if result["action"] != "manual_check_sensor" {
		t.Fatalf("action = %v; want manual_check_sensor", result["action"])
	}
	if result["requires_manual_validation"] != true || result["should_irrigate"] != nil {
		t.Fatalf("recomendação não deveria automatizar irrigação com diagnóstico persistente: %#v", result)
	}
}

func TestPredictionJSONContainsBackendBusinessFields(t *testing.T) {
	prediction := SoilMoisturePrediction{
		Model: "kalman_level_trend", TrendStatus: "stable", ConfidenceStatus: "high_uncertainty",
		Diagnostics:       SoilMoistureDiagnostics{PersistentZero: true, Messages: []string{"diagnóstico"}},
		ChangeFromCurrent: -2.5, PredictionIntervalWidth: 45,
	}
	encoded, err := json.Marshal(prediction)
	if err != nil {
		t.Fatalf("falha ao serializar projeção: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("falha ao ler JSON gerado: %v", err)
	}
	for _, key := range []string{"trend_status", "confidence_status", "diagnostics", "change_from_current", "prediction_interval_width"} {
		if _, ok := got[key]; !ok {
			t.Errorf("campo %q ausente no JSON: %s", key, encoded)
		}
	}
	diagnostics, ok := got["diagnostics"].(map[string]any)
	if !ok || diagnostics["persistent_zero"] != true {
		t.Errorf("diagnóstico de zero persistente não serializado corretamente: %s", encoded)
	}
}
