package utils

import "errors"

func MapSliceErr[T any, U any](slice []T, f func(T) (U, error)) ([]U, error) {
	result := make([]U, len(slice))
	var errs []error
	for i, s := range slice {
		var err error
		result[i], err = f(s)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}

func MapSlice[T any, U any](slice []T, f func(T) U) []U {
	result := make([]U, len(slice))
	for i, s := range slice {
		result[i] = f(s)
	}
	return result
}

func FilterSlice[T any](slice []T, f func(T) bool) []T {
	result := []T{}
	for _, s := range slice {
		if f(s) {
			result = append(result, s)
		}
	}
	return result
}
