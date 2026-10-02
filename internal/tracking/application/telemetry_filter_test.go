package application

import (
	"math"
	"testing"
	"time"
)

func TestApplyTelemetryFilter_RejectsInvalidCoordinates(t *testing.T) {
	cases := map[string][2]float64{
		"null island":  {0, 0},
		"near null":    {0.0005, -0.0005},
		"lat too high": {91, 10},
		"lon too low":  {10, -181},
		"nan":          {math.NaN(), 3},
		"inf":          {6, math.Inf(1)},
		"neg inf":      {math.Inf(-1), 3},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := applyTelemetryFilter(TelemetryConfig{}, nil, IngestLocationCommand{Latitude: c[0], Longitude: c[1]}, time.Time{}, false)
			if !r.skip || r.reason != "invalid_coordinates" {
				t.Fatalf("expected invalid_coordinates, got %+v", r)
			}
		})
	}
}

func TestApplyTelemetryFilter_AcceptsEquatorOrMeridian(t *testing.T) {
	for _, c := range [][2]float64{{0, 3.4}, {6.5, 0}, {6.5, 3.4}} {
		r := applyTelemetryFilter(TelemetryConfig{}, nil, IngestLocationCommand{Latitude: c[0], Longitude: c[1]}, time.Time{}, false)
		if r.skip {
			t.Fatalf("%v must pass, got %+v", c, r)
		}
	}
}
