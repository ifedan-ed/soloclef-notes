package pushpolicy

// Stored is one push_preferences row as a reader fetched it: the saved
// switches and the three quiet-hours columns, NULL when quiet hours are off.
type Stored struct {
	Choices          []byte
	QuietStartMinute *int16
	QuietEndMinute   *int16
	TimeZone         *string
}

// FromStored is the one reading of a stored row, for every process that
// obeys it (the push delivery, and the App Store update check that holds
// its pushes back until quiet hours end): the switches over the defaults,
// and the quiet hours when all three columns are set.
//
// A zone that no longer loads (removed from the time zone database since it
// was saved) is answered as an error beside preferences without quiet
// hours, so the caller says so in its log and still obeys the switches,
// rather than holding every push back for ever over a span it cannot read.
func FromStored(row Stored) (Preferences, error) {
	preferences := Preferences{Choices: Choices(row.Choices)}
	if row.QuietStartMinute == nil || row.QuietEndMinute == nil || row.TimeZone == nil {
		return preferences, nil
	}
	quiet, err := NewQuietHours(int(*row.QuietStartMinute), int(*row.QuietEndMinute), *row.TimeZone)
	if err != nil {
		return preferences, err
	}
	preferences.Quiet = &quiet
	return preferences, nil
}

// Defaults is the preferences of an account with no row: every kind on and
// no quiet hours.
func Defaults() Preferences {
	return Preferences{Choices: Choices(nil)}
}
