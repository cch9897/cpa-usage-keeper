package quota

import "errors"

var (
	ErrValidation      = errors.New("quota request validation failed")
	ErrNotFound        = errors.New("quota auth identity not found")
	ErrUnsupportedType = errors.New("quota identity type is unsupported")
	ErrProviderInput   = errors.New("quota provider input is invalid")
)
