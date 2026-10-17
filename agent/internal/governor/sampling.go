package governor

import (
	"context"
	"errors"
	"time"
)

// RunSampling performs an immediate sample and then one sample per tick. The
// caller owns the ticker and the runtime signal source; no polling is hidden in
// the coordinator. It returns promptly when the agent context is canceled.
func RunSampling(
	ctx context.Context,
	ticks <-chan time.Time,
	coordinator *Coordinator,
	runtimeSignals func() RuntimeSignals,
	onStep func(StepResult, error),
) error {
	if coordinator == nil || runtimeSignals == nil || ticks == nil {
		return errors.New("resource sampling requires a coordinator, signal source, and ticks")
	}
	if ctx.Err() != nil {
		return nil
	}
	step := func() {
		result, err := coordinator.Step(ctx, runtimeSignals())
		if onStep != nil {
			onStep(result, err)
		}
	}
	step()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ticks:
			if !ok {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			step()
		}
	}
}
