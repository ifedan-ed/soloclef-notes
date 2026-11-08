package principal

// Refusal is a verdict this tier reached itself: the credential was checked and
// is not acceptable, in the status and the words the Python tier words it with.
//
// It is distinct from ErrUnauthorized, which is the end of the resolution with
// nothing identified, and from ErrServedByPythonTier, which is this tier
// declining to judge at all.
type Refusal struct {
	Status  int
	Message string
}

func (r Refusal) Error() string { return r.Message }
