package core

func SelectValidator(validators []Validator) *Validator {
	if len(validators) == 0 {
		return nil
	}

	best := &validators[0]

	for i := range validators {
		if validators[i].Stake > best.Stake {
			best = &validators[i]
		}
	}

	return best
}