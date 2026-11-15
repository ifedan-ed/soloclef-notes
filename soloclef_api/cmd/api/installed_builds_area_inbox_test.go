package main

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"

func init() { registerArea(inboxArea) }

// The Inbox: what 1.0.0 and 1.1.0 ask for every time it opens -- both tabs,
// the counts the tab badge adds up, and the clears -- including the retired
// announcements tab that was a 422 and then a 500 on 2026-11-13. Ids
// 1000-1099.
var inboxArea = contractArea{
	Name:  "inbox",
	Order: 10,
	Seed: `
INSERT INTO notifications (id, user_id, type, title, message, read, metadata, created_at) VALUES
 (1001, '` + testpostgres.Alice + `', 'document_processing', 'Tides.pdf', 'Indexed', false, '{"status":"completed","document_type":"FILE"}', '2026-11-12T08:00:00Z'),
 (1002, '` + testpostgres.Alice + `', 'document_processing', 'Moon.pdf', 'Failed', false, '{"status":"failed","document_type":"FILE"}', '2026-11-11T08:00:00Z'),
 (1003, '` + testpostgres.Alice + `', 'studio_output', 'Slides ready', 'Tides', true, '{"status":"completed"}', '2026-11-10T08:00:00Z'),
 (1004, '` + testpostgres.Alice + `', 'studio_output', 'Quiz ready', 'Tides', false, '{"status":"completed"}', '2026-11-09T08:00:00Z');`,
	Cases: []contractCase{
		// inboxQueries.ts: the badge, then each tab's first page.
		{Operation: "GET /api/v1/notifications/unread-counts-batch", Path: "/api/v1/notifications/unread-counts-batch", Status: 200},
		{Operation: "GET /api/v1/notifications", Path: "/api/v1/notifications?category=status&limit=50", Status: 200},
		{Operation: "GET /api/v1/notifications", Path: "/api/v1/notifications?category=announcements&limit=50", Status: 200},
		// The next page, by date.
		{Operation: "GET /api/v1/notifications", Path: "/api/v1/notifications?category=status&limit=30&before_date=2026-11-11T08%3A00%3A00Z", Status: 200},
		{Operation: "GET /api/v1/notifications", Path: "/api/v1/notifications?category=status&filter=unread&limit=50&offset=0", Status: 200},
		{Operation: "GET /api/v1/notifications", Path: "/api/v1/notifications?category=status&filter=errors&limit=50", Status: 200},
		// Marking read, one and all.
		{Operation: "PATCH /api/v1/notifications/{notification_id}/read", Path: "/api/v1/notifications/1001/read", Status: 200},
		{Operation: "PATCH /api/v1/notifications/read-all", Path: "/api/v1/notifications/read-all", Status: 200},
		// Deleting one, then clearing each tab as the Clear button does.
		{Operation: "DELETE /api/v1/notifications/{notification_id}", Path: "/api/v1/notifications/1003", Status: 200},
		{Operation: "DELETE /api/v1/notifications", Path: "/api/v1/notifications?category=announcements&filter=unread", Status: 200},
		{Operation: "DELETE /api/v1/notifications", Path: "/api/v1/notifications?category=announcements", Status: 200},
		{Operation: "DELETE /api/v1/notifications", Path: "/api/v1/notifications?category=status&filter=unread", Status: 200},
		{Operation: "DELETE /api/v1/notifications", Path: "/api/v1/notifications?category=status", Status: 200},
	},
}
