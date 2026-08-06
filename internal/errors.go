package internal

import (
	"errors"
	"fmt"
)

type WarningErr struct {
	error
}

func Warning(err error) error {
	return WarningErr{err}
}

func Warningf(format string, args ...any) error {
	return WarningErr{error: fmt.Errorf(format, args...)}
}

func (WarningErr) Is(err error) bool {
	_, ok := err.(WarningErr)
	return ok
}

func IsWarning(err error) bool {
	return errors.Is(err, WarningErr{}) || errors.Is(err, NoopErr{})
}

type NoopErr struct {
	error
}

func Noop(err error) error {
	return NoopErr{err}
}

func IsNoopErr(err error) bool {
	return errors.Is(err, NoopErr{})
}

func (NoopErr) Is(err error) bool {
	_, ok := err.(NoopErr)
	return ok
}
