// The mail queue in Go: the job a producer writes, the transports that deliver
// it, and the sender process that drains the queue.
//
// It is a module of its own, required with a relative replace by whichever Go
// tier enqueues mail, for the reason soloclef_celery is one: the job's bytes
// have to be the bytes app/mail_queue/job.py writes, and a second copy of that
// encoder is a second spelling. soloclef_mail/AGENTS.md has the whole argument.
module git.danvics.com/danvics/soloclef/soloclef_mail

go 1.24.4

require github.com/redis/go-redis/v9 v9.22.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)
