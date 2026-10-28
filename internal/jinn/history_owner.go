package jinn

// historyOwnerEngine separates scoped execution roots from durable history identity.
func (e *Engine) historyOwnerEngine() *Engine {
	if e.historyOwner != nil {
		return e.historyOwner
	}
	return e
}
