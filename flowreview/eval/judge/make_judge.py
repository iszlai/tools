#!/usr/bin/env python3
"""Build a self-contained blind-judging page from flowreview-eval --dump files.

usage: make_judge.py OUT.html DUMP.jsonl [DUMP.jsonl ...] [--variants v1,b1-prose]
Each case shows the hunk, the case's expected facts, and every (model,
variant) output shuffled with the labels hidden. Picks persist in
localStorage; "Export" downloads them as JSON with the hidden labels.
"""
import glob, html, json, os, random, sys

args = sys.argv[1:]
variants = None
if "--variants" in args:
    i = args.index("--variants"); variants = set(args[i + 1].split(",")); del args[i:i + 2]
out, dumps = args[0], args[1:]

cases = {}
for f in glob.glob(os.path.join(os.path.dirname(__file__), "..", "cases", "*.json")):
    c = json.load(open(f)); cases[c["id"]] = c

cands = {}
for d in dumps:
    for line in open(d):
        r = json.loads(line)
        if variants and r["variant"] not in variants:
            continue
        cands.setdefault(r["case_id"], []).append(r)

def render(raw):
    try:
        o = json.loads(raw)
    except Exception:
        return {"text": "(invalid JSON) " + raw[:300], "kind": ""}
    parts = []
    if o.get("behavior"): parts.append(o["behavior"])
    if o.get("before") or o.get("after"):
        if o.get("before"): parts.append("Before: " + o["before"])
        parts.append("After: " + o.get("after", ""))
    if o.get("change"): parts.append("Change: " + o["change"])
    if o.get("what"): parts.append(o["what"])
    if o.get("inferred_why"): parts.append("Why: " + o["inferred_why"])
    if o.get("impact"): parts.append("Impact: " + o["impact"])
    if o.get("risk_flags"): parts.append("Risks: " + ", ".join(o["risk_flags"]))
    return {"text": "\n".join(parts), "kind": o.get("kind", "")}

rng = random.Random(42)
items = []
for cid in sorted(cands):
    c = cases.get(cid)
    if not c:
        continue
    cs = cands[cid][:]
    rng.shuffle(cs)
    items.append({
        "id": cid, "path": c["path"], "hunk": c["hunk"],
        "enclosing": c.get("enclosing_function", ""),
        "facts": c["expected"].get("facts", []),
        "cands": [dict(render(x["output"]), label=x["model"] + " @ " + x["variant"]) for x in cs],
    })

page = r"""<!doctype html><meta charset=utf-8><title>FlowReview summary judge</title>
<style>
:root{--bg:#fff;--fg:#1d1d1f;--muted:#6e6e73;--border:#d8d8dc;--add:#e6ffec;--del:#ffebe9;--pick:#0066cc;--card:#f6f6f8}
@media (prefers-color-scheme:dark){:root{--bg:#1e1e1e;--fg:#e8e8ea;--muted:#9b9b9f;--border:#3a3a3d;--add:#0f2a1a;--del:#2d1214;--pick:#409cff;--card:#252527}}
body{background:var(--bg);color:var(--fg);font:14px -apple-system,sans-serif;margin:0;display:flex;flex-direction:column;height:100vh}
header{display:flex;gap:8px;align-items:center;padding:8px 12px;border-bottom:1px solid var(--border)}
header .grow{flex:1}
button{font:inherit;padding:4px 10px;border:1px solid var(--border);border-radius:6px;background:var(--card);color:var(--fg);cursor:pointer}
button.on{border-color:var(--pick);outline:2px solid var(--pick)}
main{display:grid;grid-template-columns:1fr 1fr;gap:12px;padding:12px;overflow:auto;flex:1}
pre{margin:0;font:12px ui-monospace,Menlo,monospace;white-space:pre-wrap;word-break:break-word}
.hunk div{padding:0 4px}.hunk .a{background:var(--add)}.hunk .d{background:var(--del)}
.muted{color:var(--muted)} h3{margin:12px 0 4px;font-size:12px;text-transform:uppercase;color:var(--muted)}
.cand{border:1px solid var(--border);border-radius:8px;padding:10px;margin-bottom:8px;cursor:pointer;background:var(--card)}
.cand.on{border-color:var(--pick);outline:2px solid var(--pick)}
.cand .k{float:right;font-size:11px;color:var(--muted)} .cand .t{white-space:pre-wrap}
.cand .bad{float:right;margin-right:8px;font-size:11px}
textarea{width:100%;min-height:50px;font:inherit;background:var(--bg);color:var(--fg);border:1px solid var(--border);border-radius:6px}
</style>
<header><button id=prev title="Previous case (←)">◀</button><span id=pos></span><button id=next title="Next case (→)">▶</button>
<span class=grow></span><span id=done class=muted></span>
<button id=allbad title="None of these is acceptable">✗ all bad</button>
<button id=exportBtn title="Download your picks as JSON">⬇ Export</button></header>
<main><div><div class=muted id=path></div><h3>Hunk</h3><pre class=hunk id=hunk></pre>
<h3>Enclosing function</h3><pre id=enc class=muted></pre><h3>Expected facts</h3><ul id=facts></ul></div>
<div><h3>Pick the most useful summary for a reviewer (click a card)</h3><div id=cands></div>
<h3>Optional note</h3><textarea id=note placeholder="what's wrong / what you'd want instead"></textarea></div></main>
<script>
const ITEMS=__DATA__;const KEY='fr-judge-v1';
const st=JSON.parse(localStorage.getItem(KEY)||'{"i":0,"picks":{}}');
const save=()=>localStorage.setItem(KEY,JSON.stringify(st));
const esc=s=>s.replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
function show(){const it=ITEMS[st.i];const p=st.picks[it.id]||{};
 pos.textContent=`case ${st.i+1} / ${ITEMS.length}`;done.textContent=`${Object.keys(st.picks).length} judged`;
 path.textContent=it.path;
 hunk.innerHTML=it.hunk.split('\n').map(l=>`<div class="${l[0]=='+'?'a':l[0]=='-'?'d':''}">${esc(l)||' '}</div>`).join('');
 enc.textContent=it.enclosing||'(none)';facts.innerHTML=it.facts.map(f=>`<li>${esc(f)}</li>`).join('');
 cands.innerHTML=it.cands.map((c,j)=>`<div class="cand ${p.pick===j?'on':''}" data-j=${j}><span class=k>${esc(c.kind)}</span><span class=t>${esc(c.text)}</span></div>`).join('');
 allbad.classList.toggle('on',p.pick===-1);note.value=p.note||'';}
function pick(j){const it=ITEMS[st.i];const p=st.picks[it.id]||{};p.pick=j;p.label=j<0?'ALL BAD':it.cands[j].label;
 p.labels=it.cands.map(c=>c.label);st.picks[it.id]=p;save();show();}
cands.onclick=e=>{const c=e.target.closest('.cand');if(c)pick(+c.dataset.j)};
allbad.onclick=()=>pick(-1);
note.oninput=()=>{const it=ITEMS[st.i];(st.picks[it.id]=st.picks[it.id]||{}).note=note.value;save()};
prev.onclick=()=>{if(st.i>0){st.i--;save();show()}};next.onclick=()=>{if(st.i<ITEMS.length-1){st.i++;save();show()}};
document.onkeydown=e=>{if(e.target.tagName=='TEXTAREA')return;if(e.key=='ArrowLeft')prev.onclick();if(e.key=='ArrowRight')next.onclick()};
exportBtn.onclick=()=>{const a=document.createElement('a');a.href=URL.createObjectURL(new Blob([JSON.stringify(st.picks,null,1)],{type:'application/json'}));a.download='flowreview-judgements.json';a.click()};
show();
</script>"""
open(out, "w").write(page.replace("__DATA__", json.dumps(items)))
print(f"wrote {out}: {len(items)} cases, {sum(len(i['cands']) for i in items)} candidates")
