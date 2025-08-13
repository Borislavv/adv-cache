package keyword

const (
	// CtxKey is a value which must be used for extract context.Context from *fasthttp.Request.
	CtxKey = "requestCtx"
	// CtxCancelKey is a value which must be used for extract context.CancelFunc from *fasthttp.Request.
	CtxCancelKey = "requestCtxCancel"

	// ReqID is a value which must be used for extract request ID from context.Context.
	ReqID = "reqID"
	// ReqGUID is a value which must be used for extract request GUID from context.Context.
	ReqGUID = "reqGUID"
)
