package main

import (
	"testing"
	"time"
)

func TestRunAllScenarios(t *testing.T) {
	t.Parallel()

	err := run(config{
		mode:        "all",
		sessions:    8,
		roundTrips:  16,
		payloadSize: 8 * 1024,
		timeout:     20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
}
