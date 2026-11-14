package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

const (
	// maxFailures stops a run that is clearly broken (the stack is down, the token is rejected,
	// the plan breaks a catalog rule) instead of logging thousands of identical errors.
	maxFailures = 10

	// progressEvery is how many flights pass between progress lines.
	progressEvery = 1000
)

// flightCreator is what the runner needs from the API client.
type flightCreator interface {
	createFlight(ctx context.Context, spec flightSpec) (outcome, error)
}

// summary counts what a run did.
type summary struct {
	created  int
	existing int
	failed   int
	aborted  bool // stopped early because of too many failures
}

func (s summary) processed() int {
	return s.created + s.existing + s.failed
}

type result struct {
	outcome outcome
	err     error
}

// run creates the flights with a pool of workers and reports what happened.
// It returns early when the context is cancelled or after maxFailures failures, flights that were never tried are not counted.
func run(ctx context.Context, creator flightCreator, flights []flightSpec, workers int, log *slog.Logger) summary {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan flightSpec)
	results := make(chan result)

	var wg sync.WaitGroup
	for range max(workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for spec := range jobs {
				if ctx.Err() != nil {
					continue // stopping: take the rest of the jobs without working
				}
				created, err := creator.createFlight(ctx, spec)
				results <- result{outcome: created, err: err}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, spec := range flights {
			select {
			case jobs <- spec:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var s summary
	for r := range results {
		switch {
		case r.err == nil && r.outcome == outcomeCreated:
			s.created++
		case r.err == nil:
			s.existing++
		case errors.Is(r.err, context.Canceled) && ctx.Err() != nil:
			continue // cut short on purpose, not a failure
		default:
			s.failed++
			log.Warn("create flight failed", slog.Any("error", r.err))
			if s.failed >= maxFailures {
				cancel()
			}
		}

		if n := s.processed(); n%progressEvery == 0 {
			log.Info("seeding progress", slog.Int("done", n), slog.Int("total", len(flights)))
		}
	}

	s.aborted = s.failed >= maxFailures && s.processed() < len(flights)
	return s
}
