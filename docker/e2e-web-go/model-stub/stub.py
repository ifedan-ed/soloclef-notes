"""A stand-in for the model proxy, for the web end-to-end stack on Go.

The Go services reach every model through one OpenAI-shaped proxy, so one
stand-in at that address fakes all of them at once, and no service has a
test-only branch in it. (The Python stack patched a fake into each place a
model was built: tests/e2e/fakes/chat_llm.py, quick_llm.py, embeddings.py.
A new call site there was a real call until somebody remembered to patch it.
Here a new call site meets this stub or nothing: the stack has no route out.)

It is not a model. It answers by reading the request the way a test double
has to, and says only what the suite's specs wait for:

- Embeddings are made from the text's own words, so the same text gets the
  same vector and texts that share words are near each other. That is enough
  for a search to find the passage a spec just wrote.
- A chat completion is chosen from the LAST user message, by the marker a
  spec typed into it (see `chat_reply`): a fixed answer, an answer citing a
  chunk, the SCOPE-* canaries a real search returned, a call to the page
  reader. Markers never match in the system prompt or in an earlier turn.
- The short jobs (follow-up questions, a dialog's ideas, a source guide, a
  report's suggestions, a notebook's name) are told apart by the sentence
  their own prompt opens with, and answered in the JSON that prompt asks
  for, with "E2E" in every string so a spec can tell them from real text.
- A podcast's outline and lines are the smallest that are valid, so an
  Audio Overview goes the whole way: drafted, voiced, joined, stored.
- Speech is one short MP3 for every line (line.mp3 beside this file, made
  by the stack script with the worker image's own ffmpeg).
- A slide deck asked of the artifact specialist (the Studio's task says
  "Kind of artifact: pptx") is written as the real deck path expects: the
  instructions loaded, then save_artifact with a deck's content (E2E_DECK),
  which the API draws, previews and stores. A video's author is answered
  with a narrated deck (E2E_VIDEO_DECK), which the worker draws, animates,
  voices and joins. Both are answered at once, not after the specialist's
  thinking time below, so a run measures the product and not the stub.

Every request is logged to stderr in one line (what was asked, what tools
were offered, what was answered): when a spec fails, `e2e-stack-go.sh logs
model-stub` says what the product actually sent.

Standard library only; no state between requests.
"""
import hashlib
import json
import math
import os
import re
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))

# The names the stack's settings give its models (docker/e2e-web-go/
# settings.env). The stub answers for any name; these are what it lists.
MODELS = {
    "e2e-chat-model": "chat",
    "e2e-quick-model": "chat",
    "example-quick-model": "chat",
    "cloudflare-bge-m3": "embedding",
    "tts-1": "audio_speech",
}
# The tool only the artifact specialist is offered, and how long the stub
# takes to answer it (see Handler._chat).
SPECIALIST_TOOL = "save_artifact"
SPECIALIST_SECONDS = 20

# The deck and the video the stub "writes": content in the shapes
# soloclef_studio/deckspec reads, with a slide of most kinds, so the
# renderer, the preview's layout check and the video's animation all run
# on something like a real deck.
E2E_DECK = {
    "title": "E2E deck: the canary topic", "subtitle": "What the seeded source says, in eight slides", "theme": "clinical",
    "sources": [{"title": "E2E seeded source", "detail": "The Playwright suite's own document"}],
    "slides": [
        {"kind": "title", "title": "E2E deck: the canary topic", "subtitle": "What the seeded source says, in eight slides"},
        {"kind": "bullets", "title": "The canary topic has three parts", "cites": [1],
         "bullets": ["**First:** the seeded source explains it", "**Second:** the suite checks it", "**Third:** the deck shows it"],
         "notes": "E2E speaker notes for the second slide."},
        {"kind": "diagram", "style": "flow", "title": "How a deck is made now",
         "steps": [{"label": "Content", "text": "The model writes what each slide says"},
                   {"label": "Drawing", "text": "Go lays out every slide in a theme"},
                   {"label": "Preview", "text": "LibreOffice draws the PDF the viewer shows"}]},
        {"kind": "stats", "title": "E2E figures", "stats": [{"value": "1", "label": "model call writes the deck"}, {"value": "0", "label": "programs run for it"}]},
        {"kind": "comparison", "title": "Before and after", "columns": ["Before", "After"],
         "rows": [{"aspect": "Model steps", "cells": ["17 to 60", "3"]}, {"aspect": "Code run", "cells": ["python-pptx in a sandbox", "None"]}]},
        {"kind": "timeline", "title": "E2E timeline", "events": [{"label": "Step 1", "text": "The task is written"}, {"label": "Step 2", "text": "The deck is drawn"}, {"label": "Step 3", "text": "The preview is saved"}]},
        {"kind": "chart", "title": "E2E chart", "unit": "s", "caption": "Illustrative seconds per stage", "bars": [{"label": "Writing", "value": 30}, {"label": "Drawing", "value": 0.1}, {"label": "Preview", "value": 3}]},
        {"kind": "quote", "quote": "One idea per slide, with a title that states it.", "attribution": "E2E style guide"},
    ],
}
E2E_VIDEO_DECK = {
    "title": "E2E video", "theme": "classic",
    "slides": [
        {"kind": "title", "title": "E2E video: the canary topic", "subtitle": "A narrated deck",
         "narration": ["This is the end to end video.", "It is a narrated deck, drawn and animated."]},
        {"kind": "bullets", "title": "Three parts", "bullets": ["The seeded source explains it", "The suite checks it", "The video shows it"],
         "narration": ["The canary topic has three parts.", "The seeded source explains it.", "The suite checks it.", "And this video shows it."]},
        {"kind": "stats", "title": "E2E figures", "stats": [{"value": "1", "label": "model call"}, {"value": "0", "label": "browsers"}],
         "narration": ["Two numbers to end with.", "One model call wrote it.", "And no browser drew it."]},
    ],
}
DECK_TASK = "Kind of artifact: pptx"
VIDEO_AUTHOR = "You write a narrated video as a slide deck"
INSTRUCTIONS_TOOL = "load_artifact_instructions"

# The speech model's voices: OpenAI's own names, which is the roster the
# settings choose (SOLOCLEF_GO_TTS_SERVICE=openai/tts-1).
VOICES = ["alloy", "echo", "fable", "onyx", "nova", "shimmer"]
DIMENSION = int(os.environ.get("E2E_EMBEDDING_DIMENSION", "1024"))

# What the specs wait for. The same words as the Python fakes, so one suite
# reads either stack.
ANSWER = "E2E notebook answer: the seeded source explains the canary topic."
# E2E_CITE_RICH's answer: every block a copy has to carry, three sources.
RICH_ANSWER = (
    "## Findings\n\nThe canary is **CITE-ME-91** [citation:{a}]. A second source agrees "
    "[citation:{b}], and so does a page [citation:https://example.org/guide].\n\n"
    "| Item | Value |\n|---|---|\n| Dose | *5 mg* [citation:{a}] |\n\n"
    "- First point [citation:{b}]\n- Second point, with \\(r^2 \\le \\alpha\\)\n\n"
    "Code stays code: `x = [1]`."
)
# A reply that begins with this is streamed a word every SLOW_SECONDS, and
# the marker itself is not sent.
SLOW_WORDS = "\u0001slow "
SLOW_SECONDS = 0.25
EXPLAIN_ANSWER = "E2E explain answer: the notebook sources cover this in depth."
NOTHING_FOUND = "No relevant indexed content found."
# A Studio viewer's "Explain": the English templates' closing lines
# (soloclef_web/messages/en.json, "explain").
EXPLAIN_ENDINGS = (
    "Explain this topic in more detail.",
    "Help me understand more about this topic.",
    "Explain this in more detail.",
)
SCOPE_CANARY = re.compile(r"SCOPE-[A-Z]+-\d+")
# The chat's automation drafter (soloclef_assistant/automationdrafts) and
# what the stub says for the three automation markers. The question and the
# "cannot" answer are given only when the product told the model to ask and
# listed what an automation cannot do, so a spec that sees them has checked
# the product's own instructions, not just this stub.
AUTOMATION_TOOL = "create_automation"
AUTOMATION_QUESTION = "E2E question: which day and time should it run, and should I email you the result or keep it as a note?"
AUTOMATION_READY = "E2E automation draft: it is on the card for you to check and create."

NEEDS_WEB_REASON = "This task needs a web page that nobody was present to approve reading."
# The product's page reader (soloclef_assistant/pagereader.ToolName).
PAGE_READER = "web_crawl"

FOLLOWUPS = [
    "E2E follow-up: what are the key terms?",
    "E2E follow-up: how strong is the evidence?",
    "E2E follow-up: what should I read next?",
]
IDEAS = ["E2E idea %d: a seeded angle on the sources" % n for n in range(1, 10)]
GUIDE = (
    "E2E source guide: this seeded source covers the canary topic used by the "
    "Playwright suite.\n\n- E2E key topic one\n- E2E key topic two\n- E2E key topic three"
)
REPORT_SUGGESTIONS = [
    {"section": 1, "kind": "flashcards", "title": "E2E cards: rounds", "focus": "E2E focus on the first section."},
    {"section": 2, "kind": "quiz", "title": "E2E quiz: escalation", "focus": "E2E focus on the second section."},
    {"section": 3, "kind": "mindmap", "title": "E2E map: summary", "focus": "E2E focus on the third section."},
]

try:
    with open(os.path.join(HERE, "line.mp3"), "rb") as f:
        LINE_MP3 = f.read()
except OSError:
    # Said at the first speech request, not at start: only a podcast needs it.
    LINE_MP3 = b""


def vector(text):
    """A unit vector from the text's words: one dimension a word, by hash."""
    out = [0.0] * DIMENSION
    for word in re.findall(r"\w+", text.lower()):
        out[int(hashlib.sha1(word.encode()).hexdigest(), 16) % DIMENSION] += 1.0
    length = math.sqrt(sum(v * v for v in out))
    if length == 0:
        out[0] = 1.0
        return out
    return [v / length for v in out]


def text_of(message):
    content = message.get("content") or ""
    if isinstance(content, list):
        return " ".join(part.get("text", "") for part in content if isinstance(part, dict))
    return content if isinstance(content, str) else json.dumps(content)


def this_turn(messages):
    """The last user message's text and the tool results that came after it."""
    last_user = -1
    for index, message in enumerate(messages):
        if message.get("role") == "user":
            last_user = index
    if last_user < 0:
        return "", []
    results = [m for m in messages[last_user + 1:] if m.get("role") == "tool"]
    return text_of(messages[last_user]), results


def called(messages, name):
    """Whether this turn already called the tool `name`."""
    last_user = max((i for i, m in enumerate(messages) if m.get("role") == "user"), default=-1)
    for message in messages[last_user + 1:]:
        for call in message.get("tool_calls") or []:
            if (call.get("function") or {}).get("name") == name:
                return True
    return False


def quick_reply(prompt):
    """The short jobs, told apart by what their own prompt says. None: not one."""
    if '"report_suggestions"' in prompt:
        return json.dumps({"report_suggestions": REPORT_SUGGESTIONS})
    if "Name a research notebook" in prompt:
        return json.dumps({"title": "E2E Named Notebook", "icon": "\U0001f52c"})
    if "follow-up questions" in prompt:
        return json.dumps(FOLLOWUPS)
    if '"ideas"' in prompt and '"examples"' in prompt:
        return json.dumps({"ideas": IDEAS, "examples": ["E2E example focus"]})
    if "Write a source guide" in prompt:
        return GUIDE
    return None


def search_tool(offered):
    """The offered tool that searches the person's documents, if there is one."""
    for name in offered:
        if "search" in name and ("knowledge" in name or "document" in name or "source" in name or "notebook" in name):
            return name
    return None


def chat_reply(body):
    """("text", words) or ("tool", name, arguments-as-JSON)."""
    messages = body.get("messages") or []
    offered = [(t.get("function") or {}).get("name") or "" for t in body.get("tools") or []]
    system = "\n".join(text_of(m) for m in messages if m.get("role") == "system")
    asked, results = this_turn(messages)

    # The naming job is the one short job whose material is a person's
    # words: a notebook with no source yet is named from its first
    # question, which carries the spec's own marker (E2E_ANSWER ...). Its
    # instruction is the whole system prompt, so it is recognised by how
    # that prompt begins, which no chat turn's does.
    if system.startswith("Name a research notebook"):
        return ("text", quick_reply(system))

    # The deck path: instructions, then the deck's content, then a sentence.
    if SPECIALIST_TOOL in offered and DECK_TASK in asked:
        if not called(messages, INSTRUCTIONS_TOOL):
            return ("tool", INSTRUCTIONS_TOOL, json.dumps({"artifact_type": "pptx"}))
        if not called(messages, SPECIALIST_TOOL):
            return ("tool", SPECIALIST_TOOL, json.dumps({"format": "pptx", "title": E2E_DECK["title"], "deck": E2E_DECK}))
        return ("text", "Made a slide deck of eight slides: " + E2E_DECK["title"] + ".")
    if system.startswith(VIDEO_AUTHOR):
        return ("text", json.dumps(E2E_VIDEO_DECK))

    # A marker in the person's message wins over anything the prompt says:
    # a chat turn's system prompt may quote a short job's words.
    if "E2E_AUTOMATION" in asked:
        return automation_reply(body, messages, offered, system, asked, results)
    if "E2E_AUTOMATION_NEEDS_WEB" in asked:
        # Ask to read the page the task names. A run has nobody present to
        # approve the page reader, so the product refuses the call and tells
        # the model so; the answer then says what could not be done. A step
        # that was never offered the reader (documents only) just says so.
        if PAGE_READER in offered and not called(messages, PAGE_READER):
            address = re.search(r"https?://\S+", asked)
            return ("tool", PAGE_READER, json.dumps({"urls": [address.group(0).rstrip(".") if address else "https://example.org/"]}))
        return ("text", "I could not finish: " + NEEDS_WEB_REASON)
    if "E2E_KB_SCOPE" in asked:
        # Run the product's real search once, then list the canaries it
        # returned: what the answer names is what the turn could reach. The
        # words after the marker go into the query, so a spec that shares
        # its account with other tests can ask for a word only it wrote.
        tool = search_tool(offered)
        if tool and not results:
            rest = asked.split("E2E_KB_SCOPE", 1)[1].splitlines()[0].strip()
            return ("tool", tool, json.dumps({"query": ("SCOPE canary " + rest).strip()}))
        found = sorted(set(SCOPE_CANARY.findall(" ".join(text_of(r) for r in results))))
        return ("text", "E2E scope answer: " + (" ".join(found) or "none"))
    rich = re.search(r"E2E_CITE_RICH:(\d+),(\d+)", asked)
    if rich:
        # A formatted answer citing two passages and a web page, the first
        # passage twice: what a copy must keep (heading, bold, list, table,
        # code, maths) and number (1, 2, 3 in order of first appearance).
        return ("text", RICH_ANSWER.format(a=rich.group(1), b=rich.group(2)))
    if "E2E_CITE_LABELS" in asked:
        # Cite as a real model does: search, then write the labels the
        # search numbered ("[7]"), which the API saves as markers when the
        # turn ends. The answer streams slowly (SLOW_WORDS), so a spec can
        # look at the chips while it is still streaming.
        tool = search_tool(offered)
        if tool and not results:
            rest = asked.split("E2E_CITE_LABELS", 1)[1].splitlines()[0].strip()
            return ("tool", tool, json.dumps({"query": rest or "canary"}))
        labels = []
        for found in re.findall(r"^\s*\[(\d+)\]", "\n".join(text_of(r) for r in results), re.M):
            if found not in labels:
                labels.append(found)
        if not labels:
            return ("text", NOTHING_FOUND)
        # The last-found first, then the first, then the last again: the
        # numbers on screen must follow the answer, not the search.
        first, last = labels[0], labels[-1]
        return ("text", SLOW_WORDS + "Streamed claim one [%s] and claim two [%s], and one again [%s]." % (last, first, last))
    cite = re.search(r"E2E_CITE:(\d+)", asked)
    if cite:
        return ("text", "%s Grounded in the notebook source [citation:%s]." % (ANSWER, cite.group(1)))
    if "E2E_ANSWER" in asked:
        return ("text", ANSWER)
    if any(ending in asked for ending in EXPLAIN_ENDINGS):
        return ("text", EXPLAIN_ANSWER)

    # A podcast's outline and its lines (the worker's drafting prompts name
    # the JSON they want): one short segment, one line a speaker, so an
    # Audio Overview a spec starts is really drafted, voiced and joined.
    if '{"segments": [{"title"' in system:
        return ("text", json.dumps({"segments": [{
            "title": "E2E segment: the seeded source",
            "talking_points": ["what the seeded source says"], "target_words": 40}]}))
    if '{"turns": [{"speaker"' in system:
        slots = [int(s) for s in re.findall(r"- slot (\d+) —", system)] or [0]
        return ("text", json.dumps({"turns": [
            {"speaker": slot, "text": "E2E speaker %d reads from the seeded source." % slot} for slot in slots]}))

    quick = quick_reply(system + "\n" + asked)
    if quick is not None:
        return ("text", quick)
    return ("text", NOTHING_FOUND)


def tool_description(body, name):
    """The description the product gave the offered tool `name`, or ""."""
    for t in body.get("tools") or []:
        function = t.get("function") or {}
        if function.get("name") == name:
            return function.get("description") or ""
    return ""


def automations_block(system):
    """The system prompt's <automations> block, or ""."""
    found = re.search(r"<automations>(.*?)</automations>", system, re.S)
    return found.group(1) if found else ""


def automation_reply(body, messages, offered, system, asked, results):
    """The three automation markers.

    E2E_AUTOMATION_DRAFT drafts an automation the first time (a Monday
    08:00 schedule in the zone the prompt says the device is in), then says
    the card is ready. E2E_AUTOMATION_VAGUE asks a clarifying question, but
    only if the tool's description tells the model to ask. E2E_AUTOMATION_CANNOT
    answers with the prompt's own "cannot" lines about sub-minute schedules
    and sending mail.
    """
    block = automations_block(system)
    if AUTOMATION_TOOL not in offered or not block:
        return ("text", "E2E: no automation drafter was offered")
    if "E2E_AUTOMATION_VAGUE" in asked:
        if "ask one short question" in tool_description(body, AUTOMATION_TOOL) and "clarifying question" in block:
            return ("text", AUTOMATION_QUESTION)
        return ("text", "E2E: nothing told the model to ask")
    if "E2E_AUTOMATION_CANNOT" in asked:
        lines = [line[2:] for line in block.split("An automation cannot:", 1)[-1].splitlines() if line.startswith("- ")]
        said = [line for line in lines if "once a minute" in line or "Send email to anyone" in line]
        return ("text", "E2E: an automation cannot do all of that. " + " ".join(said))
    if not called(messages, AUTOMATION_TOOL):
        zone = re.search(r"device is in the time zone ([A-Za-z_/+-]+)\.", block)
        name = re.search(r"E2E_AUTOMATION_DRAFT ([^\n]+)", asked)
        return ("tool", AUTOMATION_TOOL, json.dumps({
            "name": name.group(1).strip() if name else "E2E weekly digest",
            "description": "E2E: Monday summary of what arrived",
            "task": "Summarise the documents that arrived this week, in five bullet points.",
            "schedule": {"cron": "0 8 * * 1", "timezone": zone.group(1) if zone else "UTC"},
        }))
    if any("automation_draft_id=" in text_of(r) for r in results):
        return ("text", AUTOMATION_READY)
    return ("text", "E2E: the draft was refused: " + " ".join(text_of(r) for r in results)[:300])


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        # The access line says nothing the request line below does not.
        pass

    def _note(self, line):
        sys.stderr.write("stub %s\n" % line)
        sys.stderr.flush()

    def _send(self, status, content_type, raw):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _json(self, status, body):
        self._send(status, "application/json", json.dumps(body).encode())

    def do_GET(self):
        path = self.path.split("?")[0]
        if path == "/health":
            return self._json(200, {"status": "ok"})
        if path == "/v1/models":
            return self._json(200, {"object": "list", "data": [{"id": name, "object": "model"} for name in MODELS]})
        if path == "/v1/model/info":
            # LiteLLM's own route: what each model is for, and, for the
            # speech model, the voices it speaks. The API offers a podcast
            # only the voices listed here (soloclef_assistant/voices), and
            # refuses to make one for a speech model that lists none.
            return self._json(200, {"data": [
                {"model_name": name, "model_info": {"mode": mode, **({"voices": VOICES} if mode == "audio_speech" else {})}}
                for name, mode in MODELS.items()]})
        self._note("GET %s -> 404" % self.path)
        self._json(404, {"error": {"message": "no such route: " + self.path}})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        path = self.path.split("?")[0]
        if path == "/v1/embeddings":
            texts = body.get("input")
            if isinstance(texts, str):
                texts = [texts]
            return self._json(200, {"object": "list", "model": body.get("model"), "data": [
                {"object": "embedding", "index": i, "embedding": vector(t)} for i, t in enumerate(texts)],
                "usage": {"prompt_tokens": 1, "total_tokens": 1}})
        if path == "/v1/audio/speech":
            if not body.get("input") or not body.get("voice") or not body.get("model"):
                return self._json(400, {"error": {"message": "model, voice and input are required"}})
            if not LINE_MP3:
                return self._json(500, {"error": {"message": "the stub has no line.mp3; the stack script makes it"}})
            return self._send(200, "audio/mpeg", LINE_MP3)
        if path == "/v1/chat/completions":
            return self._chat(body)
        self._note("POST %s -> 404" % self.path)
        self._json(404, {"error": {"message": "no such route: " + self.path}})

    def _chat(self, body):
        reply = chat_reply(body)
        offered = [(t.get("function") or {}).get("name") for t in body.get("tools") or []]
        asked_now, _ = this_turn(body.get("messages") or [])
        if SPECIALIST_TOOL in offered and DECK_TASK not in asked_now:
            # The artifact specialist (a quiz, a mind map, flashcards): this
            # stub cannot write one, so the run will end "nothing made". It
            # ends after a real model's thinking time and not at once, so
            # that a page shows its "Generating" state long enough to be
            # looked at, and a spec can finish the output itself through
            # the seed route while the run is still in progress. (The
            # run's own ending is then ignored: the product writes it only
            # over a job still running.)
            time.sleep(SPECIALIST_SECONDS)
        asked, _ = this_turn(body.get("messages") or [])
        self._note("chat model=%s stream=%s tools=%s asked=%r -> %s %r" % (
            body.get("model"), bool(body.get("stream")), offered, asked[:120], reply[0], reply[1][:120]))
        usage = {"prompt_tokens": 5, "completion_tokens": 11, "total_tokens": 16}
        call = None
        if reply[0] == "tool":
            # One id a call, different for each tool, as a model's are.
            call = {"id": "call_e2e_" + reply[1], "type": "function",
                    "function": {"name": reply[1], "arguments": reply[2]}}
        if not body.get("stream"):
            message = {"role": "assistant", "content": reply[1]} if call is None else {
                "role": "assistant", "content": None, "tool_calls": [call]}
            return self._json(200, {
                "id": "e2e", "object": "chat.completion", "model": body.get("model"),
                "choices": [{"index": 0, "finish_reason": "stop" if call is None else "tool_calls", "message": message}],
                "usage": usage})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Connection", "close")
        self.end_headers()

        def frame(obj):
            self.wfile.write(b"data: " + json.dumps(obj).encode() + b"\n\n")
            self.wfile.flush()

        base = {"id": "e2e", "object": "chat.completion.chunk", "model": body.get("model")}
        frame({**base, "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}]})
        if call is None:
            # A word at a time: the page under test renders a stream, and
            # one frame holding the whole answer would not be one.
            text, slow = reply[1], reply[1].startswith(SLOW_WORDS)
            if slow:
                text = text[len(SLOW_WORDS):]
            words = text.split(" ")
            for index, word in enumerate(words):
                piece = word if index == len(words) - 1 else word + " "
                frame({**base, "choices": [{"index": 0, "delta": {"content": piece}, "finish_reason": None}]})
                if slow:
                    time.sleep(SLOW_SECONDS)
            finish = "stop"
        else:
            frame({**base, "choices": [{"index": 0, "delta": {"tool_calls": [{"index": 0, **call}]}, "finish_reason": None}]})
            finish = "tool_calls"
        frame({**base, "choices": [{"index": 0, "delta": {}, "finish_reason": finish}]})
        frame({**base, "choices": [], "usage": usage})
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()
        self.close_connection = True


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 4000), Handler).serve_forever()
