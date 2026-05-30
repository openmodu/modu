package subagent

import "context"

type depthContextKey struct{}

func subagentDepth(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	depth, _ := ctx.Value(depthContextKey{}).(int)
	if depth < 0 {
		return 0
	}
	return depth
}

func withSubagentDepth(ctx context.Context, depth int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if depth < 0 {
		depth = 0
	}
	return context.WithValue(ctx, depthContextKey{}, depth)
}
