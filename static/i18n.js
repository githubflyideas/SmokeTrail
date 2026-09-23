// Console translation, applied in the browser rather than rendered server-side.
//
// The pages are static files embedded in the binary, so there is no template
// layer to hang a language on — and doing it client-side means switching
// language is instant and does not cost a round trip or a session. The catalogue
// itself is the same i18n/strings.json the notification-area icon reads; only the
// delivery differs.
//
// Markup contract:
//   data-i18n="key"        replaces textContent
//   data-i18n-ph="key"     replaces the placeholder attribute
//   data-i18n-title="key"  replaces the title attribute
//   data-i18n-picker       is replaced by a <select> of the available languages
//
// For strings built in JavaScript, T('key') after i18nReady.

(function () {
  const STORE = 'smoketrail_lang';
  let S = {};          // key -> translated string
  let META = { langs: [], names: {}, lang: 'en' };

  // An explicit choice wins over the browser's preference, and is remembered per
  // browser. Without one we send no lang and let the server read Accept-Language,
  // which is the answer the user already configured once and should not have to
  // configure again.
  function chosen() {
    try { return localStorage.getItem(STORE) || ''; } catch { return ''; }
  }
  function remember(tag) {
    try { tag ? localStorage.setItem(STORE, tag) : localStorage.removeItem(STORE); } catch {}
  }

  window.T = function (key, fallback) {
    return (S && S[key]) || fallback || key;
  };

  function applyTo(root) {
    root.querySelectorAll('[data-i18n]').forEach(el => {
      const v = S[el.getAttribute('data-i18n')];
      if (v) el.textContent = v;
    });
    root.querySelectorAll('[data-i18n-ph]').forEach(el => {
      const v = S[el.getAttribute('data-i18n-ph')];
      if (v) el.setAttribute('placeholder', v);
    });
    root.querySelectorAll('[data-i18n-title]').forEach(el => {
      const v = S[el.getAttribute('data-i18n-title')];
      if (v) el.setAttribute('title', v);
    });
  }

  function buildPicker() {
    document.querySelectorAll('[data-i18n-picker]').forEach(host => {
      host.textContent = '';
      const sel = document.createElement('select');
      sel.className = 'lang-picker';
      sel.setAttribute('aria-label', 'Language');
      for (const tag of META.langs) {
        const o = document.createElement('option');
        o.value = tag;
        o.textContent = META.names[tag] || tag;
        o.selected = tag === META.lang;
        sel.append(o);
      }
      sel.onchange = () => { remember(sel.value); load(sel.value); };
      host.append(sel);
    });
  }

  async function load(tag) {
    const q = tag ? ('?lang=' + encodeURIComponent(tag)) : '';
    try {
      const res = await fetch('/api/i18n' + q);
      if (!res.ok) return;
      const d = await res.json();
      S = d.s || {};
      META = { langs: d.langs || [], names: d.names || {}, lang: d.lang || 'en' };
    } catch { return; }

    document.documentElement.lang = META.lang;
    applyTo(document);
    buildPicker();
    // Pages that build text in script redraw themselves here rather than each
    // one re-implementing "did the language change".
    document.dispatchEvent(new CustomEvent('i18n', { detail: META }));
  }

  window.i18nApply = applyTo;
  window.i18nReady = load(chosen());
})();
