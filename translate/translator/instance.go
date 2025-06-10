package translator

import "context"

type Instance interface {
	Translate(context.Context, TranslateRequest) (*TranslateResponse, error)
	Name() string
}
