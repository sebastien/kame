package cli

// ProgressEstimate is advisory, not a graph-completion or freshness assertion.
type ProgressEstimate struct {
	Known       bool
	RemainingMS int64
	Percent     int
}

// Estimate uses inclusive historical target wall times. Taking the maximum
// remaining duration, not their sum, avoids double-counting parallel subtrees.
// Unknown or overrun work is indeterminate until a useful estimate is available.
func (s *ProgressState) Estimate(nowNS int64) ProgressEstimate {
	started := nowNS
	remaining := int64(0)
	active := 0
	for i := range s.Outcomes {
		o := s.Outcomes[i]
		if o.Started && o.ProgressStartedNS < started {
			started = o.ProgressStartedNS
		}
		if !o.Started || o.Terminal {
			continue
		}
		active++
		elapsed := (nowNS - o.ProgressStartedNS) / 1000000
		if elapsed < 0 || o.EstimateMS <= elapsed {
			return ProgressEstimate{}
		}
		left := o.EstimateMS - elapsed
		if left > remaining {
			remaining = left
		}
	}
	if active == 0 {
		return ProgressEstimate{}
	}
	elapsed := (nowNS - started) / 1000000
	percent := int(100 * elapsed / (elapsed + remaining))
	if percent > 99 {
		percent = 99
	}
	return ProgressEstimate{Known: true, RemainingMS: remaining, Percent: percent}
}
