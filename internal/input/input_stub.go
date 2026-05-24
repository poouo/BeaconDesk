//go:build !windows

package input

import "errors"

func NewInjector() (Injector, error) {
	return nil, errors.New("input injection is only planned for Windows clients")
}
