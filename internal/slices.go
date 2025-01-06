package internal

import "slices"

func Find[S ~[]E, E any](slice S, fn func(E) bool) (E, bool) {
	switch idx := slices.IndexFunc(slice, fn); idx {
	case -1:
		var zero E
		return zero, false
	default:
		return slice[idx], true
	}
}

func FindAll[S ~[]E, E any](slice S, fn func(E) bool) []E {
	var result []E
	for _, elem := range slice {
		if fn(elem) {
			result = append(result, elem)
		}
	}
	return result
}
