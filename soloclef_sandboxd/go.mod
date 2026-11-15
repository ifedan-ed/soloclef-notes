// The stack's sandbox service: it holds the Docker socket and nothing else,
// starts one locked-down container per chat thread or per render attempt from
// the stack's sandbox image, runs commands in it through `docker exec`, moves
// files in and out the same way, and removes it when it has been idle too
// long. The API and the worker reach it over HTTP with a key
// (soloclef_studio/sandbox is their client). It replaced the OpenSandbox
// server (Python, two extra images, a published port per sandbox).
//
// A module of its own, with no requirements: the Engine API calls it makes
// are a handful of JSON requests on a Unix socket, which the standard library
// does in a page (docker/), where Docker's own SDK would bring dozens of
// modules for the same few calls.
module git.danvics.com/danvics/soloclef/soloclef_sandboxd

go 1.25.0
