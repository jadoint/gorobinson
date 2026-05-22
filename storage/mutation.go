package storage

type countMutation struct {
	Count  TokenCount
	Upsert bool
	Delete bool
}

func applyCountMutation(current TokenCount, rowExists bool, count int64, isSpam, isLearn bool) countMutation {
	if !rowExists && !isLearn {
		return countMutation{}
	}

	delta := count
	if !isLearn {
		delta = -delta
	}

	if isSpam {
		current.CountSpam += delta
	} else {
		current.CountHam += delta
	}
	if current.CountHam < 0 {
		current.CountHam = 0
	}
	if current.CountSpam < 0 {
		current.CountSpam = 0
	}

	if current.CountHam == 0 && current.CountSpam == 0 {
		if rowExists {
			return countMutation{Count: current, Delete: true}
		}
		return countMutation{}
	}

	return countMutation{Count: current, Upsert: true}
}
