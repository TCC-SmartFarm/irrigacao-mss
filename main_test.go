package main

import "testing"

func TestKalmanSmoothsRepeatedSoilReadings(t *testing.T) {
	got := kalman([]float64{80, 100, 80}, 1, 4)
	if len(got) != 3 {
		t.Fatalf("expected 3 outputs, got %d", len(got))
	}
	if got[1] <= 80 || got[1] >= 100 {
		t.Fatalf("second estimate should smooth the spike; got %.2f", got[1])
	}
	if got[2] <= 80 || got[2] >= got[1] {
		t.Fatalf("third estimate should move back toward 80; got %.2f", got[2])
	}
}

func TestKalmanSingleReadingUnchanged(t *testing.T) {
	got := kalman([]float64{42}, 1, 4)
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("single reading should remain unchanged; got %v", got)
	}
}
