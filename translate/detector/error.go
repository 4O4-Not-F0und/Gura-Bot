package detector

import "errors"

func newWeakError(err error) *WeakError {
	return &WeakError{
		Err: err,
	}
}

type WeakError struct {
	Err error
}

func (e *WeakError) Error() string {
	return e.Err.Error()
}

func CheckWeakError(err error) bool {
	var weakErr = new(WeakError)
	return errors.As(err, &weakErr)
}
