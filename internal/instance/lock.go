package instance

import "errors"

var ErrAlreadyRunning = errors.New("another PayLessForAI instance is already running")

type Lock struct {
	release func() error
}

func (l *Lock) Close() error {
	if l == nil || l.release == nil {
		return nil
	}
	return l.release()
}
