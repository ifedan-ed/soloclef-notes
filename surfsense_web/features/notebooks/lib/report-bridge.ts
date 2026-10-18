/**
 * The contents bridge for an interactive (HTML) report.
 *
 * The report runs in a sandboxed, opaque-origin frame, so the reading view
 * cannot read its DOM. This script runs inside the frame, after the report,
 * and talks to the viewer by postMessage: it sends the report's section
 * headings (the same rule as report-outline.ts: skip a leading h1 title, list
 * the shallowest repeated level), then which one is being read as the report
 * scrolls; it scrolls to a section when the viewer asks. The viewer accepts
 * messages only from that frame's window and shows the headings as text.
 */

export const BRIDGE_FROM_REPORT = "surfsense-report";
export const BRIDGE_TO_REPORT = "surfsense-report-viewer";
/** How far below the top a heading must pass to count as the one being read. */
export const READING_LINE_PX = 96;

export type BridgeMessage =
	| { source: typeof BRIDGE_FROM_REPORT; type: "toc"; items: string[] }
	| { source: typeof BRIDGE_FROM_REPORT; type: "active"; index: number };

export const REPORT_BRIDGE_SCRIPT = `(function(){
var FROM=${JSON.stringify(BRIDGE_FROM_REPORT)},TO=${JSON.stringify(BRIDGE_TO_REPORT)},LINE=${READING_LINE_PX};
var heads=[],last=-2,sent="";
function visible(el){return !!(el.offsetWidth||el.offsetHeight||el.getClientRects().length);}
function pick(){
  var all=[].slice.call(document.querySelectorAll("h1,h2,h3,h4")).filter(visible);
  if(all.length&&all[0].tagName==="H1")all=all.slice(1);
  var counts={};all.forEach(function(h){counts[h.tagName]=(counts[h.tagName]||0)+1;});
  var levels=Object.keys(counts).sort(),level=null;
  for(var i=0;i<levels.length;i++){if(counts[levels[i]]>=2){level=levels[i];break;}}
  if(!level)level=levels[0];
  return all.filter(function(h){return h.tagName===level;});
}
function label(h){return (h.textContent||"").replace(/\\s+/g," ").trim().slice(0,160);}
function post(msg){msg.source=FROM;try{parent.postMessage(msg,"*");}catch(e){}}
function toc(){heads=pick();var items=heads.map(label),key=JSON.stringify(items);
  if(key!==sent){sent=key;last=-2;post({type:"toc",items:items});}spy();}
function spy(){if(!heads.length)return;var active=0;
  var root=document.scrollingElement||document.documentElement;
  var bottom=root.scrollTop+root.clientHeight>=root.scrollHeight-4&&root.scrollHeight>root.clientHeight;
  if(bottom){active=heads.length-1;}else{for(var i=0;i<heads.length;i++){if(heads[i].getBoundingClientRect().top<=LINE)active=i;else break;}}
  if(active!==last){last=active;post({type:"active",index:active});}}
var queued=false;
document.addEventListener("scroll",function(){if(queued)return;queued=true;requestAnimationFrame(function(){queued=false;spy();});},true);
window.addEventListener("message",function(e){var d=e.data;if(e.source!==parent||!d||d.source!==TO)return;
  if(d.type==="goto"&&heads[d.index]){heads[d.index].scrollIntoView({behavior:"smooth",block:"start"});}
  if(d.type==="hello"){sent="";toc();}});
if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",toc);else toc();
window.addEventListener("load",toc);setTimeout(toc,600);
})();`;

/** A message from the report frame, or null for anything else. */
export function parseBridgeMessage(data: unknown): BridgeMessage | null {
	if (!data || typeof data !== "object") return null;
	const message = data as Record<string, unknown>;
	if (message.source !== BRIDGE_FROM_REPORT) return null;
	if (message.type === "toc" && Array.isArray(message.items)) {
		const items = message.items
			.filter((item): item is string => typeof item === "string")
			.slice(0, 200)
			.map((item) => item.slice(0, 160));
		return { source: BRIDGE_FROM_REPORT, type: "toc", items };
	}
	if (
		message.type === "active" &&
		typeof message.index === "number" &&
		Number.isInteger(message.index) &&
		message.index >= 0
	) {
		return { source: BRIDGE_FROM_REPORT, type: "active", index: message.index };
	}
	return null;
}
