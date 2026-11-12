package routing

import "testing"

// Only the tasks that declare it ignore their result, and everything else
// must come back false, because a producer that guessed would write a
// header nothing asked for.
func TestOnlyTheTasksThatDeclareItIgnoreTheirResult(t *testing.T) {
	if !IgnoresResult("notebook_autoname") {
		t.Error("notebook_autoname declares ignore_result and was not recognised")
	}
	for _, task := range []string{
		"surfsense", "delete_folder_documents_background",
		"podcast.draft_transcript", "push_send", "",
	} {
		if IgnoresResult(task) {
			t.Errorf("%s does not declare ignore_result", task)
		}
	}
}
