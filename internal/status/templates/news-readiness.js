(() => {
  const button = document.querySelector('.mode-button[data-mode="news"]');
  if (!button) return;

  const defaultLabel = button.textContent.trim() || 'ニュース連続';
  const card = document.getElementById('mode-card');
  let stateEl = document.getElementById('news-engine-state');
  if (!stateEl && card) {
    stateEl = document.createElement('div');
    stateEl.id = 'news-engine-state';
    stateEl.setAttribute('aria-live', 'polite');
    stateEl.style.cssText = [
      'width:100%',
      'font:700 11px var(--font-mono)',
      'color:var(--soft)',
      'margin-top:2px',
      'line-height:1.45'
    ].join(';');
    card.appendChild(stateEl);
  }

  function renderNewsReadiness(snapshot) {
    const ready = !!snapshot.news_ready;
    const count = Number(snapshot.news_ready_count || 0);
    const state = snapshot.news_state || (ready ? 'ready' : 'loading');

    button.disabled = !ready;
    button.setAttribute('aria-disabled', ready ? 'false' : 'true');
    button.style.opacity = ready ? '1' : '.5';
    button.style.cursor = ready ? 'pointer' : 'not-allowed';

    if (ready) {
      button.textContent = defaultLabel;
      button.title = count > 0 ? `放送準備済みニュース ${count} 件` : 'ニュース放送の準備ができています';
      if (stateEl) stateEl.textContent = `NEWS ENGINE · READY${count > 0 ? ` · ${count}件先読み済み` : ''}`;
      return;
    }

    if (state === 'unavailable') {
      button.textContent = 'ニュース利用不可';
      button.title = 'RSSまたは音声生成の設定を確認してください';
      if (stateEl) stateEl.textContent = 'NEWS ENGINE · 利用不可';
      return;
    }

    if (state === 'waiting') {
      button.textContent = 'ニュース待機中…';
      button.title = '新しいニュースを確認しています';
      if (stateEl) stateEl.textContent = 'NEWS ENGINE · 新着確認中';
      return;
    }

    button.textContent = 'ニュース準備中…';
    button.title = 'RSS取得・音声生成・BGM合成をバックグラウンドで実行しています';
    if (stateEl) stateEl.textContent = 'NEWS ENGINE · RSS / TTS / BGM を準備中';
  }

  // The main player already receives /events, but this tiny independent SSE
  // keeps the enhancement decoupled from the large legacy template. It can be
  // removed later when the UI is split into proper modules.
  fetch('/now-playing', {cache: 'no-store'})
    .then(r => r.ok ? r.json() : Promise.reject())
    .then(renderNewsReadiness)
    .catch(() => {});

  const events = new EventSource('/events');
  events.onmessage = event => {
    try { renderNewsReadiness(JSON.parse(event.data)); } catch (_) {}
  };
})();
