package nextcloudimport

import "git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"

// The account and the file entry are package nextcloud's
// (soloclef_connectors/nextcloud), the client the chat's Nextcloud tools
// read with too. They are named here in the import's own words because a
// plan, a job and a stored row each carry one, and the import's code reads
// as an import with them.
type (
	Account = nextcloud.Account
	Entry   = nextcloud.Entry
)
