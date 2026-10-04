package contrast

import (
	"context"
	"math"
)

func nilCtx() context.Context { return context.Background() }
func absF(x float64) float64  { return math.Abs(x) }
