"""The self-contained HTML section that embeds a notebook's Studio outputs.

Everything is rendered here, deterministically, rather than retyped by the
report model: content is escaped once, in one place, and reads without script
(outlines and cards are native ``<details>``, the quiz is plain fieldsets).
A small inline script only adds the one-card-at-a-time deck, answer checking
and outline controls. Players carry ``data-surfsense-media`` instead of a
``src``: the artifact viewer resolves those to signed links for the signed-in
viewer, so no media is inlined and nothing plays for someone without access.
"""

from __future__ import annotations

import re
from html import escape

from .collect import MediaEmbed, StudioEmbeds, StudyEmbed

# The report fragment carries this attribute exactly when the section is in it.
EMBED_MARKER = "data-surfsense-studio-embeds"
SECTION_ID = "studio-outputs"

_LIST_ITEM = re.compile(r"^(?P<indent> *)[-*+] +(?P<label>.+?)\s*$")

_STYLE = """
.ss-embeds{--ss-ink:#1f2328;--ss-muted:#59636e;--ss-line:#d1d9e0;--ss-soft:#f6f8fa;--ss-accent:#0969da;--ss-good:#1a7f37;--ss-bad:#cf222e;color:var(--ss-ink);font:inherit;margin:2.5rem 0 0;padding-top:1.5rem;border-top:1px solid var(--ss-line)}
.ss-embeds *{box-sizing:border-box}
.ss-embeds h2{font-size:1.5rem;margin:0 0 .25rem}
.ss-embeds h3{font-size:1.15rem;margin:0 0 .75rem}
.ss-embeds .ss-intro{color:var(--ss-muted);margin:0 0 1.5rem}
.ss-embeds .ss-block{border:1px solid var(--ss-line);border-radius:12px;padding:1rem;margin:0 0 1.25rem;background:#fff}
.ss-embeds .ss-sub{color:var(--ss-muted);font-size:.875rem;margin:-.5rem 0 .75rem}
.ss-embeds button{font:inherit;min-height:2.5rem;padding:.4rem .9rem;border:1px solid var(--ss-line);border-radius:8px;background:var(--ss-soft);color:var(--ss-ink);cursor:pointer}
.ss-embeds button:hover{background:#eef1f4}
.ss-embeds button:focus-visible,.ss-embeds summary:focus-visible,.ss-embeds input:focus-visible{outline:2px solid var(--ss-accent);outline-offset:2px}
.ss-embeds .ss-row{display:flex;flex-wrap:wrap;gap:.5rem;align-items:center;margin:.75rem 0 0}
.ss-embeds .ss-tree ul{list-style:none;margin:0;padding-left:1.1rem;border-left:1px solid var(--ss-line)}
.ss-embeds .ss-tree>ul{padding-left:0;border-left:0}
.ss-embeds .ss-tree li{margin:.2rem 0}
.ss-embeds .ss-tree summary{cursor:pointer;padding:.15rem 0}
.ss-embeds .ss-tree .ss-leaf{padding:.15rem 0 .15rem 1.05rem}
.ss-embeds .ss-cards{list-style:none;margin:0;padding:0;display:grid;gap:.5rem}
.ss-embeds .ss-card{border:1px solid var(--ss-line);border-radius:10px;padding:1rem;background:var(--ss-soft)}
.ss-embeds .ss-card summary{cursor:pointer;font-weight:600;white-space:pre-wrap}
.ss-embeds .ss-card .ss-back{margin:.75rem 0 0;white-space:pre-wrap}
.ss-embeds .ss-card .ss-hint{color:var(--ss-muted);font-size:.875rem;margin:.5rem 0 0;white-space:pre-wrap}
.ss-embeds .ss-q{border:1px solid var(--ss-line);border-radius:10px;padding:1rem;margin:0 0 .75rem}
.ss-embeds .ss-q legend{font-weight:600;padding:0 .25rem;white-space:pre-wrap}
.ss-embeds .ss-q label{display:flex;gap:.5rem;align-items:flex-start;padding:.35rem 0;cursor:pointer;white-space:pre-wrap}
.ss-embeds .ss-q input{margin-top:.3rem;min-width:1rem;min-height:1rem}
.ss-embeds .ss-feedback{margin:.5rem 0 0;white-space:pre-wrap}
.ss-embeds .ss-feedback.ss-good{color:var(--ss-good)}
.ss-embeds .ss-feedback.ss-bad{color:var(--ss-bad)}
.ss-embeds .ss-score{font-weight:600}
.ss-embeds .ss-scroll{overflow:auto;max-height:28rem;border:1px solid var(--ss-line);border-radius:8px}
.ss-embeds table{border-collapse:collapse;width:100%;font-size:.875rem}
.ss-embeds th,.ss-embeds td{border-bottom:1px solid var(--ss-line);padding:.4rem .6rem;text-align:left;vertical-align:top}
.ss-embeds th{position:sticky;top:0;background:var(--ss-soft)}
.ss-embeds audio{width:100%}
.ss-embeds video{width:100%;max-height:70vh;background:#000;border-radius:8px}
.ss-embeds .ss-missing{color:var(--ss-muted);font-size:.875rem;margin:.5rem 0 0}
@media (prefers-reduced-motion:reduce){.ss-embeds *{transition:none!important;animation:none!important}}
"""

_SCRIPT = """
(function(){
var root=document.getElementById("studio-outputs");if(!root)return;
function all(sel,el){return Array.prototype.slice.call((el||root).querySelectorAll(sel));}
all("[data-ss-tree]").forEach(function(tree){
  all("[data-ss-expand]",tree.parentNode).forEach(function(b){b.hidden=false;b.addEventListener("click",function(){
    var open=b.getAttribute("data-ss-expand")==="1";all("details",tree).forEach(function(d){d.open=open;});});});
});
all("[data-ss-deck]").forEach(function(deck){
  var cards=all(".ss-card",deck);if(!cards.length)return;var i=0;
  var nav=deck.parentNode.querySelector("[data-ss-deck-nav]");var count=nav.querySelector("[data-ss-count]");
  var tmpl=count.getAttribute("data-ss-template");
  function show(){cards.forEach(function(c,k){c.hidden=k!==i;if(k!==i)c.querySelector("details").open=false;});
    count.textContent=tmpl.replace("{n}",String(i+1)).replace("{total}",String(cards.length));}
  nav.hidden=false;
  nav.querySelector("[data-ss-prev]").addEventListener("click",function(){i=(i+cards.length-1)%cards.length;show();});
  nav.querySelector("[data-ss-next]").addEventListener("click",function(){i=(i+1)%cards.length;show();});
  nav.querySelector("[data-ss-flip]").addEventListener("click",function(){var d=cards[i].querySelector("details");d.open=!d.open;});
  show();
});
all("[data-ss-quiz]").forEach(function(quiz){
  var qs=all(".ss-q",quiz);var score=quiz.querySelector("[data-ss-score]");var tmpl=score.getAttribute("data-ss-template");
  function tally(){var done=0,good=0;qs.forEach(function(q){if(q.getAttribute("data-ss-done")){done++;if(q.getAttribute("data-ss-done")==="1")good++;}});
    score.hidden=done===0;score.textContent=tmpl.replace("{correct}",String(good)).replace("{total}",String(qs.length));}
  qs.forEach(function(q){
    var btn=q.querySelector("[data-ss-check]");var fb=q.querySelector(".ss-feedback");btn.hidden=false;
    btn.addEventListener("click",function(){
      var picked=q.querySelector("input:checked");if(!picked)return;
      var ok=picked.value===q.getAttribute("data-ss-answer");
      fb.hidden=false;fb.className="ss-feedback "+(ok?"ss-good":"ss-bad");
      fb.textContent=(ok?q.getAttribute("data-ss-right"):q.getAttribute("data-ss-wrong"))+"\\n"+q.getAttribute("data-ss-why");
      q.setAttribute("data-ss-done",ok?"1":"0");tally();});
  });
});
all("[data-surfsense-media]").forEach(function(player){
  var note=player.parentNode.querySelector(".ss-missing");
  function missing(){player.hidden=true;if(note)note.hidden=false;}
  if(!player.getAttribute("src")){missing();return;}
  player.addEventListener("error",missing);
});
})();
"""


def _text(value: str) -> str:
    return escape(value, quote=True)


def _outline(markdown: str) -> tuple[str, list[tuple[int, str]]]:
    title = ""
    items: list[tuple[int, str]] = []
    for line in markdown.splitlines():
        if line.startswith("# ") and not title:
            title = line[2:].strip()
            continue
        match = _LIST_ITEM.match(line)
        if match:
            items.append((len(match["indent"]) // 2, match["label"]))
    return title, items


def _tree(items: list[tuple[int, str]], start: int, depth: int) -> tuple[str, int]:
    parts = ["<ul>"]
    index = start
    while index < len(items) and items[index][0] >= depth:
        level, label = items[index]
        if level > depth:
            # A skipped level still renders, under the previous item's depth.
            level = depth
        has_children = index + 1 < len(items) and items[index + 1][0] > level
        if has_children:
            children, index = _tree(items, index + 1, level + 1)
            parts.append(
                f"<li><details open><summary>{_text(label)}</summary>"
                f"{children}</details></li>"
            )
        else:
            parts.append(f'<li class="ss-leaf">{_text(label)}</li>')
            index += 1
    parts.append("</ul>")
    return "".join(parts), index


def _mindmap(embed: StudyEmbed, labels: dict[str, str]) -> str:
    title, items = _outline(str(embed.payload))
    tree = _tree(items, 0, 0)[0] if items else ""
    root = _text(title or embed.title)
    return (
        f'<div class="ss-block"><h3>{_text(labels["mindmap"])}</h3>'
        f'<p class="ss-sub">{_text(embed.title)}</p>'
        f'<div class="ss-tree" data-ss-tree><details open><summary><strong>{root}'
        f"</strong></summary>{tree}</details></div>"
        '<div class="ss-row">'
        f'<button type="button" data-ss-expand="1" hidden>{_text(labels["expand_all"])}</button>'
        f'<button type="button" data-ss-expand="0" hidden>{_text(labels["collapse_all"])}</button>'
        "</div></div>"
    )


def _flashcards(embed: StudyEmbed, labels: dict[str, str]) -> str:
    cards = []
    for front, back, hint in embed.payload:  # type: ignore[union-attr]
        hint_html = (
            f'<p class="ss-hint">{_text(labels["hint"])}: {_text(hint)}</p>'
            if hint
            else ""
        )
        cards.append(
            f'<li class="ss-card"><details><summary>{_text(front)}</summary>'
            f'<p class="ss-back">{_text(back)}</p>{hint_html}</details></li>'
        )
    return (
        f'<div class="ss-block"><h3>{_text(labels["flashcards"])}</h3>'
        f'<p class="ss-sub">{_text(embed.title)}</p>'
        f'<ol class="ss-cards" data-ss-deck>{"".join(cards)}</ol>'
        '<div class="ss-row" data-ss-deck-nav hidden>'
        f'<button type="button" data-ss-prev>{_text(labels["previous"])}</button>'
        f'<button type="button" data-ss-flip>{_text(labels["flip"])}</button>'
        f'<button type="button" data-ss-next>{_text(labels["next"])}</button>'
        f'<span data-ss-count aria-live="polite" data-ss-template="{_text(labels["card_of"])}"></span>'
        "</div></div>"
    )


def _quiz(embed: StudyEmbed, labels: dict[str, str], section: int) -> str:
    questions = []
    for number, (question, options, correct, why) in enumerate(
        embed.payload,  # type: ignore[arg-type]
        start=1,
    ):
        name = f"ss-q{section}-{number}"
        choices = "".join(
            f'<label><input type="radio" name="{name}" value="{index}">'
            f"<span>{_text(option)}</span></label>"
            for index, option in enumerate(options)
        )
        wrong = labels["incorrect"].replace("{answer}", options[correct])
        questions.append(
            f'<fieldset class="ss-q" data-ss-answer="{correct}" '
            f'data-ss-right="{_text(labels["correct"])}" '
            f'data-ss-wrong="{_text(wrong)}" data-ss-why="{_text(why)}">'
            f"<legend>{_text(labels['question'].replace('{n}', str(number)))}: "
            f"{_text(question)}</legend>{choices}"
            f'<div class="ss-row"><button type="button" data-ss-check hidden>'
            f"{_text(labels['check'])}</button></div>"
            '<p class="ss-feedback" aria-live="polite" hidden></p></fieldset>'
        )
    return (
        f'<div class="ss-block" data-ss-quiz><h3>{_text(labels["quiz"])}</h3>'
        f'<p class="ss-sub">{_text(embed.title)}</p>{"".join(questions)}'
        f'<p class="ss-score" data-ss-score aria-live="polite" hidden '
        f'data-ss-template="{_text(labels["score"])}"></p></div>'
    )


def _table(embed: StudyEmbed, labels: dict[str, str]) -> str:
    rows: list[list[str]] = embed.payload  # type: ignore[assignment]
    head, *body = rows
    header = "".join(f'<th scope="col">{_text(cell)}</th>' for cell in head)
    lines = "".join(
        "<tr>" + "".join(f"<td>{_text(cell)}</td>" for cell in row) + "</tr>"
        for row in body
    )
    return (
        f'<div class="ss-block"><h3>{_text(labels["data_table"])}</h3>'
        f'<p class="ss-sub">{_text(embed.title)}</p>'
        f'<div class="ss-scroll" tabindex="0"><table><thead><tr>{header}</tr></thead>'
        f"<tbody>{lines}</tbody></table></div></div>"
    )


def _media(kind: str, embed: MediaEmbed, labels: dict[str, str]) -> str:
    reference = f"{embed.artifact_id}:{embed.file_id}"
    if kind == "video":
        player = (
            f'<video controls preload="metadata" playsinline '
            f'data-surfsense-media="{reference}" title="{_text(embed.title)}"></video>'
        )
    else:
        player = (
            f'<audio controls preload="metadata" '
            f'data-surfsense-media="{reference}" title="{_text(embed.title)}"></audio>'
        )
    return (
        f'<div class="ss-block"><h3>{_text(labels[kind])}</h3>'
        f'<p class="ss-sub">{_text(embed.title)}</p>{player}'
        f'<p class="ss-missing" hidden>{_text(labels["media_unavailable"])}</p></div>'
    )


def render_embeds(embeds: StudioEmbeds, labels: dict[str, str]) -> str | None:
    """The section, or ``None`` when the notebook has nothing to embed."""
    if embeds.empty:
        return None
    blocks: list[str] = []
    if embeds.video:
        blocks.append(_media("video", embeds.video, labels))
    if embeds.audio:
        blocks.append(_media("audio", embeds.audio, labels))
    if embeds.mindmap:
        blocks.append(_mindmap(embeds.mindmap, labels))
    if embeds.flashcards:
        blocks.append(_flashcards(embeds.flashcards, labels))
    if embeds.quiz:
        blocks.append(_quiz(embeds.quiz, labels, section=1))
    if embeds.data_table:
        blocks.append(_table(embeds.data_table, labels))
    return (
        f'<section id="{SECTION_ID}" class="ss-embeds" {EMBED_MARKER} '
        f'aria-labelledby="{SECTION_ID}-title">'
        f"<style>{_STYLE.strip()}</style>"
        f'<h2 id="{SECTION_ID}-title">{_text(labels["section"])}</h2>'
        f'<p class="ss-intro">{_text(labels["intro"])}</p>'
        f"{''.join(blocks)}"
        f"<script>{_SCRIPT.strip()}</script></section>"
    )
