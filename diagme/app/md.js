// mdlite — the smallest markdown that is still worth reading.
//
// This file lives in mdlite/ and is COPIED into each tool that uses it
// (diagme/app/md.js, taskhound/md.js). Edit it here and run
// `make -C mdlite install`; the copies are byte-identical and `make -C mdlite
// check` fails if one has drifted. They are copies rather than references
// because neither consumer can reach outside its own directory: Go's embed
// refuses `..` and will not follow a symlink, and nginx only serves diagme/app.
//
// Exposes a single global, `md(src) -> html`, and also exports for node so the
// tests can run without a browser.
//
// Deliberately not supported: links, images, tables, blockquotes, nested lists,
// setext headings. Every one of those is easy to add and none of them has been
// needed; add them when something actually wants them.

(function (root) {
  'use strict';

  var esc = function (s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  };

  // Everything is escaped before it is emitted, on every path, so source text
  // can never inject markup. That matters: descriptions arrive over HTTP.
  function md(src) {
    var inline = function (t) {
      return esc(t)
        .replace(/`([^`]+)`/g, '<code>$1</code>')
        .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
        .replace(/\*([^*]+)\*/g, '<em>$1</em>');
    };

    var out = '', list = null, inCode = false, codeBuf = [], para = [];

    // Consecutive plain lines are one paragraph, as markdown says they are.
    // Prose here is hard-wrapped, so treating every line as its own <p> would
    // put a paragraph break after every 78 characters. Joining before the
    // inline pass also lets **emphasis** span a wrap.
    var closePara = function () {
      if (para.length) { out += '<p>' + inline(para.join(' ')) + '</p>'; para = []; }
    };
    var closeList = function () { if (list) { out += '</' + list + '>'; list = null; } };
    var closeBlock = function () { closePara(); closeList(); };
    var openList = function (kind) {
      closePara();
      if (list !== kind) { closeList(); out += '<' + kind + '>'; list = kind; }
    };

    var lines = String(src == null ? '' : src).split('\n');
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];

      if (line.trim().indexOf('```') === 0) {
        if (inCode) {
          out += '<pre>' + esc(codeBuf.join('\n')) + '</pre>';
          codeBuf = [];
          inCode = false;
        } else {
          closeBlock();
          inCode = true;
        }
        continue;
      }
      if (inCode) { codeBuf.push(line); continue; }

      var h = line.match(/^(#{1,3})\s+(.*)/);
      if (h) {
        closeBlock();
        out += '<h' + h[1].length + '>' + inline(h[2]) + '</h' + h[1].length + '>';
        continue;
      }

      // Task lists, because acceptance criteria are most of what gets written.
      // The boxes are for reading, not clicking: tick them by editing the text.
      var task = line.match(/^\s*[-*]\s+\[([ xX])\]\s+(.*)/);
      if (task) {
        openList('ul');
        var done = task[1].toLowerCase() === 'x';
        out += '<li class="task' + (done ? ' done' : '') + '">' +
               '<input type="checkbox" disabled' + (done ? ' checked' : '') + '>' +
               inline(task[2]) + '</li>';
        continue;
      }

      var ul = line.match(/^\s*[-*]\s+(.*)/);
      if (ul) { openList('ul'); out += '<li>' + inline(ul[1]) + '</li>'; continue; }

      var ol = line.match(/^\s*\d+[.)]\s+(.*)/);
      if (ol) { openList('ol'); out += '<li>' + inline(ol[1]) + '</li>'; continue; }

      if (line.trim() === '') { closeBlock(); continue; }
      closeList();
      para.push(line.trim());
    }

    if (inCode) out += '<pre>' + esc(codeBuf.join('\n')) + '</pre>';
    closeBlock();
    return out;
  }

  root.md = md;
  if (typeof module !== 'undefined' && module.exports) module.exports = { md: md, esc: esc };
})(typeof globalThis !== 'undefined' ? globalThis : this);
