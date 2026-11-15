package studiogenerate

import (
	"context"
	"fmt"
	"net/http"
)

// One account's share of the Studio's places. The places are few (four
// runs per API process, cmd/api's studioConcurrentRuns) and every account
// shares them, so without a share one person who queued twenty outputs
// would hold all of them for as long as twenty take, and a run whose
// account is at its sandbox limit holds its place while it waits for one
// (deliverables.UnattendedSandboxWait, up to ten minutes).

// runsPerAccount is how many of one account's artifact jobs run at once,
// across every replica (ClaimQueuedJobs counts them in the database).
//
// Two: half of one process's four places, so a second account's job
// starts at the next free place even while the first account has many
// waiting; and two of a person's own outputs still go side by side (a
// quiz made while a report builds). Two is also below the deployment's
// sandbox limit per account (SOLOCLEF_API_SANDBOX_MAX_PER_ACCOUNT, three
// here) with a place to spare for the person's chat, so a run is not
// normally made to wait for a sandbox behind its own account's other run.
const runsPerAccount = 2

// maxUnfinishedPerAccount is how many artifact jobs one account may have
// waiting or being made at once. A further request is refused, said in a
// sentence, until one finishes.
//
// Ten: the Studio offers seven kinds that are made here (a quiz,
// flashcards, a mind map, an infographic, slides, a data table and a
// report), so a person asking for one of each, and a couple more variants,
// is under it; what is beyond it is a script or a held-down button, and
// would only lengthen that account's own wait. With two made at a time
// (runsPerAccount) and a run taking from under a minute to a few, ten is a
// queue that empties within the quarter hour.
const maxUnfinishedPerAccount = 10

// tooManyUnfinished is the refusal, in the words the dialog shows.
var tooManyUnfinished = fmt.Sprintf("You already have %d Studio outputs waiting or being made. Start another when one of them is ready.", maxUnfinishedPerAccount)

// admitAnother refuses a new artifact job when the account already has its
// share waiting or being made. It is called inside the transaction that
// queues the job, and holds the account's lock (LockAccountStudioJobs) to
// the end of it, so two requests at once are counted one after the other.
func admitAnother(ctx context.Context, q *Queries, userID string) error {
	if err := q.LockAccountStudioJobs(ctx, userID); err != nil {
		return err
	}
	unfinished, err := q.UnfinishedArtifactJobs(ctx, userID)
	if err != nil {
		return err
	}
	if unfinished >= maxUnfinishedPerAccount {
		return &refusal{http.StatusTooManyRequests, tooManyUnfinished}
	}
	return nil
}
