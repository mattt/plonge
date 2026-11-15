package disk

// Change is the free space on a volume before and after an operation.
// Other processes, and snapshots that keep the blocks of deleted files,
// can change free space too, so the change is not only the operation's.
type Change struct {
	FreeBefore int64 `json:"free_before"`
	FreeAfter  int64 `json:"free_after"`
	// GainedBytes is FreeAfter minus FreeBefore; it is negative when free space fell.
	GainedBytes int64 `json:"gained_bytes"`
	// Error says why free space could not be measured; the numbers are then 0.
	Error string `json:"error,omitempty"`
}

// free reads free space; tests replace it.
var free = Free

// Track measures the free space on path's volume before and after fn runs.
// It runs fn even when it cannot measure free space.
func Track(path string, fn func()) Change {
	before, errBefore := free(path)
	fn()
	if errBefore != nil {
		return Change{Error: errBefore.Error()}
	}
	after, err := free(path)
	if err != nil {
		return Change{Error: err.Error()}
	}
	return Change{FreeBefore: before, FreeAfter: after, GainedBytes: after - before}
}
