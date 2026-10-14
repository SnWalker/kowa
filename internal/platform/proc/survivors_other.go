//go:build !darwin && !linux

package proc

// scanSurvivors is unsupported on this platform: marker-based discovery of
// escaped worker descendants has no portable implementation, so the Runner
// reports that residual scanning is unavailable instead of claiming isolation.
func scanSurvivors(string) ([]survivor, error) {
	return nil, nil
}
