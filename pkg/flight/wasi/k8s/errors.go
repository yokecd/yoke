package k8s

import "errors"

type ErrorNotFound string

func (err ErrorNotFound) Error() string { return string(err) }

func (ErrorNotFound) Is(target error) bool {
	_, ok := target.(ErrorNotFound)
	return ok
}

func IsErrNotFound(err error) bool {
	return errors.Is(err, ErrorNotFound(""))
}

type ErrorUnauthenticated string

func (err ErrorUnauthenticated) Error() string { return string(err) }

func (ErrorUnauthenticated) Is(target error) bool {
	_, ok := target.(ErrorUnauthenticated)
	return ok
}

func IsErrUnauthenticated(err error) bool {
	return errors.Is(err, ErrorUnauthenticated(""))
}

type ErrorForbidden string

func (err ErrorForbidden) Error() string { return string(err) }

func (ErrorForbidden) Is(target error) bool {
	_, ok := target.(ErrorForbidden)
	return ok
}

func IsErrForbidden(err error) bool {
	return errors.Is(err, ErrorForbidden(""))
}

var ErrorClusterAccessNotGranted = errors.New("access to the cluster has not been granted for this flight invocation")
