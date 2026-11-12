"""Print the frames the Python service writes, with its own encoder.

The two .sse files beside this script are its output. They are the expected
bytes for the Go writer's tests: json.dumps at its defaults, which is what
VercelStreamingService._format_sse calls, so they are the wire as it stands.

Regenerate with:  python3 python_frames.py
"""

import json
from datetime import datetime, timezone
from pathlib import Path


def sse(payload):
    return "data: " + json.dumps(payload) + "\n\n"


def stamp(*args):
    return datetime(*args, tzinfo=timezone.utc).isoformat()


# Characters chosen to exercise every escaping rule: an accent, a symbol, an
# emoji outside the basic plane, DEL, a control character, a tab, a backslash
# and the line separator. Built with chr() so this file stays plain ASCII.
AWKWARD = (
    "Caf" + chr(0xE9) + " " + chr(0x2615) + " na" + chr(0xEF) + "ve " + chr(0x1F600)
    + " " + chr(0x7F) + chr(0x01) + "\t\\ end" + chr(0x2028)
)
ETE = chr(0xE9) + "t" + chr(0xE9)
DASH = chr(0x2014)

turn = [
    sse({"type": "start", "messageId": "msg_1"}),
    sse({"type": "start-step"}),
    sse({"type": "data-turn-info", "data": {"chat_turn_id": "turn-9"}}),
    sse({"type": "reasoning-start", "id": "reasoning_1", "startedAt": stamp(2026, 11, 11, 9, 30, 0, 250000)}),
    sse({"type": "reasoning-delta", "id": "reasoning_1", "delta": 'Hmm, "quotes" & <tags>\n'}),
    sse({"type": "reasoning-end", "id": "reasoning_1", "completedAt": stamp(2026, 11, 11, 9, 30, 1)}),
    sse({"type": "text-start", "id": "text_2"}),
    sse({"type": "text-delta", "id": "text_2", "delta": AWKWARD}),
    sse({"type": "text-end", "id": "text_2"}),
    sse({"type": "tool-input-start", "toolCallId": "call_a", "toolName": "search", "langchainToolCallId": "call_a"}),
    sse({"type": "tool-input-delta", "toolCallId": "call_a", "inputTextDelta": '{"q":'}),
    sse({
        "type": "tool-input-available", "toolCallId": "call_a", "toolName": "search",
        "input": {"q": ETE, "n": [1, 2, {"deep": True}], "none": None},
        "langchainToolCallId": "call_a",
    }),
    sse({
        "type": "tool-output-available", "toolCallId": "call_a",
        "output": {"status": "completed", "result_length": 12},
        "langchainToolCallId": "call_a",
    }),
    sse({"type": "data-interrupt-request", "data": {
        "action_requests": [{"name": "delete", "args": {"id": 7}}],
        "review_configs": [{"action_name": "delete", "allowed_decisions": ["approve", "edit", "reject", "approve_always"]}],
        "interrupt_type": "permission_ask",
        "context": {"mcp_server": "Files"},
        "tool_call_id": "call_b",
    }}),
    sse({"type": "finish-step"}),
    sse({"type": "finish"}),
    "data: [DONE]\n\n",
]

failure = [
    sse({"type": "error", "message": "Something went wrong " + DASH + " try again", "errorCode": "SERVER_ERROR"}),
    "data: [DONE]\n\n",
]

here = Path(__file__).parent
(here / "python_turn.sse").write_text("".join(turn), encoding="ascii")
(here / "python_failure.sse").write_text("".join(failure), encoding="ascii")
