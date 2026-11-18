package domain

import "errors"

var (
	ErrUnknownModule     = errors.New("unknown module")
	ErrUnavailable       = errors.New("module unavailable in installation")
	ErrDependency        = errors.New("module dependency conflict")
	ErrHouseholdRequired = errors.New("authenticated household required")
)
