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

# The speech model's voices: OpenAI's own names, which is the roster the
# settings choose (SOLOCLEF_GO_TTS_SERVICE=openai/tts-1).
VOICES = ["alloy", "echo", "fable", "onyx", "nova", "shimmer"]
DIMENSION = int(os.environ.get("E2E_EMBEDDING_DIMENSION", "1024"))

# What the specs wait for. The same words as the Python fakes, so one suite
# reads either stack.
ANSWER = "E2E notebook answer: the seeded source explains the canary topic."
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

    # A marker in the person's message wins over anything the prompt says:
    # a chat turn's system prompt may quote a short job's words.
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
        if SPECIALIST_TOOL in offered:
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
            words = reply[1].split(" ")
            for index, word in enumerate(words):
                piece = word if index == len(words) - 1 else word + " "
                frame({**base, "choices": [{"index": 0, "delta": {"content": piece}, "finish_reason": None}]})
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
