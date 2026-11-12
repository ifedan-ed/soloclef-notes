-- The statements of a thread's action log. Each names the owner twice: on
-- the thread and on the row. A row's own user_id is not enough (a row could
-- point at a thread that is not its account's), and the thread's is not
-- either (a thread's log must never show a row recorded for someone else).

-- name: ThreadActions :many
-- One page of a thread's actions, newest first, each with the action that
-- undid it (if one did) and the size of the whole log beside it, so the
-- page, its badges and its total are one statement.
SELECT a.id, a.thread_id, a.user_id, a.tool_name, a.args, a.result_id, a.reversible,
       a.reverse_descriptor, a.error, a.reverse_of, a.tool_call_id, a.chat_turn_id, a.created_at,
       undo.id AS reverted_by_action_id,
       count(*) OVER () AS total
FROM agent_action_log a
JOIN new_chat_threads t ON t.id = a.thread_id AND t.user_id = sqlc.arg(user_id)
LEFT JOIN agent_action_log undo ON undo.reverse_of = a.id AND undo.user_id = a.user_id
WHERE a.thread_id = sqlc.arg(thread_id) AND a.user_id = sqlc.arg(user_id)
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: ThreadLogSize :one
-- Asked only when a page comes back empty: whether the thread is the
-- caller's at all (a 404 if not), and how long its log is.
SELECT EXISTS (SELECT 1 FROM new_chat_threads t WHERE t.id = sqlc.arg(thread_id) AND t.user_id = sqlc.arg(user_id)) AS owned,
       (SELECT count(*) FROM agent_action_log a
        JOIN new_chat_threads t ON t.id = a.thread_id AND t.user_id = sqlc.arg(user_id)
        WHERE a.thread_id = sqlc.arg(thread_id) AND a.user_id = sqlc.arg(user_id)) AS total;
