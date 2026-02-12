package llm

func Complete(model *Model, ctx *Context, opts *StreamOptions) (*AssistantMessage, error) {
	s, err := Stream(model, ctx, opts)
	if err != nil {
		return nil, err
	}
	return s.Result()
}

func CompleteSimple(model *Model, ctx *Context, opts *SimpleStreamOptions) (*AssistantMessage, error) {
	s, err := StreamSimple(model, ctx, opts)
	if err != nil {
		return nil, err
	}
	return s.Result()
}
