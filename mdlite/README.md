# mdlite — markdown small enough to read in one sitting

~90 lines, no dependencies, no build step, no CDN. Works in a browser and in
node. It exists because two tools in this repo needed the same small renderer
and neither could justify pulling in a real one.

```js
md('# Title\n- [x] done\n- [ ] todo')
// <h1>Title</h1><ul><li class="task done">…</li><li class="task">…</li></ul>
```

```bash
make test     # 9 tests, node only
make install  # push md.js into every consumer
make check    # test, then fail if a consumer's copy has drifted
```

## What it does

| Syntax | Renders as |
|---|---|
| `# ## ###` | `<h1> <h2> <h3>` — three levels, anything deeper stays prose |
| `**bold**` `*italic*` `` `code` `` | `<strong> <em> <code>` |
| ` ``` ` fences | `<pre>`, contents verbatim |
| `- a` / `* a` | `<ul><li>` |
| `1. a` / `1) a` | `<ol><li>` |
| `- [ ] a` / `- [x] a` | `<li class="task">` with a disabled checkbox |

Task lists are the reason this is not just diagme's original renderer: issue
descriptions are mostly acceptance criteria. The boxes are for reading, not
clicking — tick them by editing the text.

Deliberately absent: links, images, tables, blockquotes, nested lists, setext
headings. Each is easy to add and none has been needed. Add one when something
actually wants it.

## Escaping

Every path escapes before it emits — headings, list items, inline spans, code
fences. That is not decoration: taskhound renders descriptions that arrived over
HTTP, so `<script>alert(1)</script>` in a description has to come out as text.
`test.js` asserts this on each path, and that test is the one to keep passing if
you change anything.

## Why the consumers hold copies

`make install` copies `md.js` into `diagme/app/` and `taskhound/`. That looks
redundant, and it is not:

- Go's `embed` refuses a `..` pattern and will not follow a symlink, so
  taskhound cannot reach a file outside its own directory.
- nginx serves `diagme/app` and nothing above it.

So a copy is the only thing that works without giving both tools a build step,
which would cost more than it saves. The copies are byte-identical, `make check`
fails if one has drifted, and taskhound additionally asserts it in
`TestEmbeddedRendererMatchesMdlite` — that copy is compiled into released
binaries, so stale there means stale in a release.

Edit `mdlite/md.js`, never a copy.
