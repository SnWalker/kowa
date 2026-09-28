// Package clock provides the production wall clock behind an injectable port.
package clock

import "time"

type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }
