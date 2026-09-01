(() => {
  const state = { requests: [], models: [], modelStats: [], providerStats: [], groupStats: [], groups: [], summary: {}, network: {}, view: 'overview', detailDrawer: null, modelSort: { key: null, direction: null }, modelFilters: { model: [], provider: [], tags: [], input: [], output: [] }, filterPopover: null };
  const $ = (selector) => document.querySelector(selector);
  const $$ = (selector) => Array.from(document.querySelectorAll(selector));
  const formatNumber = (value) => new Intl.NumberFormat('en-US').format(Number(value || 0));
  const formatCompact = (value) => new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 }).format(Number(value || 0));
  const formatUSD = (pico) => {
    const value = Number(pico || 0) / 1e12;
    if (!Number.isFinite(value)) return '—';
    const rounded = new Intl.NumberFormat('en-US', { maximumSignificantDigits: 3, useGrouping: false }).format(value);
    return `$${rounded}`;
  };
  const formatUSDPerMillion = (pico) => formatUSD(Number(pico || 0) * 1e6);
  const formatSignedUSD = (pico) => { const value = Number(pico || 0); if (value === 0) return '$0'; return `${value < 0 ? '-' : '+'}${formatUSD(Math.abs(value))}`; };
  const discountPercent = (bps) => `${Math.max(0, Math.min(100, Math.round(Number(bps || 0) / 100)))}%`;
  const cacheHitPercent = (summary) => { const input = Number(summary?.input_tokens || 0); const cached = Number(summary?.cached_read_tokens || 0); const total = input + cached; return total > 0 ? `${Math.round(cached * 100 / total)}%` : '0%'; };
  const modelDiscount = (bps) => bps == null ? '—' : discountPercent(bps);
  const modelDiscountDetail = (model) => model.discount_percent_bps == null ? 'no reference baseline' : `input ${discountPercent(model.discount_input_percent_bps)} · output ${discountPercent(model.discount_output_percent_bps)} vs reference`;
  function appendModelPriceLine(container, model, key, label) {
    const line = document.createElement('span'); line.className = 'price-line';
    const kind = document.createElement('span'); kind.className = 'price-kind'; kind.textContent = label;
    const current = Number(model.pricing?.[key] || 0); const official = Number(model.official_pricing?.[key] || 0);
    line.append(kind);
    if (official > current && current >= 0) {
      const original = document.createElement('span'); original.className = 'price-original'; original.textContent = formatUSDPerMillion(official);
      const arrow = document.createElement('span'); arrow.className = 'price-arrow'; arrow.textContent = '→';
      const discounted = document.createElement('span'); discounted.className = 'price-current'; discounted.textContent = formatUSDPerMillion(current);
      line.append(original, arrow, discounted);
    } else {
      const value = document.createElement('span'); value.className = 'price-current'; value.textContent = formatUSDPerMillion(current); line.append(value);
    }
    container.append(line);
  }
  const discountUnavailableLabel = (request) => {
    if (request.official_cost_pico_usd == null || Number(request.official_cost_pico_usd) <= 0) return 'no official price';
    if (request.actual_cost_pico_usd == null) return 'no actual price';
    return 'not available';
  };
  const discountAmount = (request) => request.discount_pico_usd == null ? null : Math.max(0, Number(request.discount_pico_usd));
  const discountLabel = (request) => { const amount = discountAmount(request); return amount == null ? `— · ${discountUnavailableLabel(request)}` : amount === 0 ? '$0' : `${formatUSD(amount)} · ${discountPercent(request.discount_percent_bps)} saved`; };
  const shortID = (value) => value ? `${value.slice(0, 8)}…${value.slice(-4)}` : '—';
  const protocolName = (value) => ({ chat_completions: 'Chat Completions', responses: 'Responses', anthropic_messages: 'Anthropic Messages' }[value] || value || '—');
  const providerName = (value) => value === 'surplus' ? 'Surplus Intelligence' : value === 'openrouter' ? 'OpenRouter' : value || 'Unknown provider';
  const dateValue = (value) => value ? new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '—';
  const formatDuration = (ms) => { if (ms == null || !Number.isFinite(Number(ms))) return '—'; const value = Number(ms); if (value < 1000) return `${Math.max(0, Math.round(value))} ms`; const seconds = value / 1000; return `${seconds.toFixed(seconds < 10 ? 1 : 0)} s`; };
  function tokenBreakdown(input, output, total, cached, reasoning) {
    const wrapper = document.createElement('span'); wrapper.className = 'token-breakdown';
    const flow = document.createElement('span'); flow.className = 'token-flow';
    const chunk = (value, label) => { const item = document.createElement('span'); item.className = 'token-chunk'; const number = document.createElement('span'); number.className = 'token-number'; number.textContent = formatCompact(value); const suffix = document.createElement('small'); suffix.className = 'token-label'; suffix.textContent = label; item.append(number, suffix); return item; };
    flow.append(chunk(input, 'in')); const separator = document.createElement('span'); separator.className = 'token-separator'; separator.textContent = '/'; flow.append(separator, chunk(output, 'out'));
    const note = document.createElement('small'); note.className = 'token-total'; note.textContent = `${formatCompact(total)} total${Number(cached || 0) ? ` · ${formatCompact(cached)} cached` : ''}${Number(reasoning || 0) ? ` · ${formatCompact(reasoning)} reasoning` : ''}`;
    wrapper.append(flow, note); return wrapper;
  }
  function setTokenMetric(selector, input, output, total, cached, reasoning) { const element = $(selector); if (!element) return; element.replaceChildren(tokenBreakdown(input, output, total, cached, reasoning)); }

  async function fetchJSON(url, options) {
    if (url === '/api/providers/credentials' && options?.body) { try { const body = JSON.parse(options.body); body.access_mode = $('#provider-access-mode')?.value || body.access_mode || 'api'; body.subscription_fee_usd = $('#subscription-fee')?.value || body.subscription_fee_usd || ''; options = { ...options, body: JSON.stringify(body) }; } catch (_) {} }
    if (options && options.method && options.method !== 'GET' && options.method !== 'HEAD') { const token = document.cookie.split(';').map((item) => item.trim()).find((item) => item.startsWith('plai_csrf='))?.slice('plai_csrf='.length); if (token) options = { ...options, headers: { ...(options.headers || {}), 'X-CSRF-Token': decodeURIComponent(token) } }; }
    const response = await fetch(url, options);
    const payload = await response.json();
    if (!response.ok) { const error = new Error(payload?.error?.message || `Request failed (${response.status})`); error.payload = payload; error.status = response.status; throw error; }
    return payload;
  }

  function setText(selector, value) { const element = $(selector); if (element) element.textContent = value; }
  function appendTextCell(row, value, className) { const cell = document.createElement('td'); if (className) cell.className = className; cell.textContent = value; row.append(cell); return cell; }
  function stateBadge(value) { const badge = document.createElement('span'); badge.className = `state-badge ${value || 'received'}`; badge.textContent = value || 'received'; return badge; }
  function providerBadge(provider) { const badge = document.createElement('span'); badge.className = 'badge provider'; badge.textContent = providerName(provider); return badge; }
  const modalityIcons = {
    text: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5h14M12 5v14M8 19h8"/></svg>',
    image: '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="8" cy="9" r="2" fill="currentColor" stroke="none"/><path d="m5 17 4.5-4 3.5 3 2.5-2.5L20 17" fill="currentColor" stroke="none"/></svg>',
    video: '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="13" height="14" rx="2"/><path d="m16 10 5-3v10l-5-3z"/><path d="m8 9 4 3-4 3z" fill="currentColor" stroke="none"/></svg>',
    file: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h8l4 4v14H6z" fill="currentColor" fill-opacity=".18"/><path d="M14 3v5h4M9 12h6M9 16h6"/></svg>',
    audio: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 9h4l5-4v14l-5-4H4z" fill="currentColor" stroke="none"/><path d="M16 9a4 4 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11"/></svg>'
  };
  const modalityLabels = { text: 'Text', image: 'Image', video: 'Video', file: 'File', audio: 'Audio' };
  function modalityDisplay(input, output) {
    const wrapper = document.createElement('span'); wrapper.className = 'modality-display'; wrapper.setAttribute('aria-label', `Input: ${(input || []).join(', ') || 'unknown'}; output: ${(output || []).join(', ') || 'unknown'}`);
    const group = (values) => { const container = document.createElement('span'); container.className = 'modality-group'; (values || []).forEach((value) => { const name = String(value).toLowerCase(); const label = modalityLabels[name] || name.charAt(0).toUpperCase() + name.slice(1); const icon = document.createElement('span'); icon.className = `modality-icon modality-${name}`; icon.innerHTML = modalityIcons[name] || '<span aria-hidden="true">•</span>'; icon.title = label; icon.setAttribute('aria-label', label); icon.setAttribute('data-tooltip', label); icon.setAttribute('role', 'img'); icon.tabIndex = 0; container.append(icon); }); return container; };
    wrapper.append(group(input)); const arrow = document.createElement('span'); arrow.className = 'modality-arrow'; arrow.textContent = '→'; wrapper.append(arrow, group(output)); return wrapper;
  }

  async function loadStatus() {
    try {
      const data = await fetchJSON('/api/status');
      const pill = $('#status-pill');
      pill.classList.toggle('offline', !data.ready);
      setText('#status', data.ready ? `Ready · ${formatNumber(data.model_count)} models` : 'Starting · database not ready');
      setText('#metric-total-note', data.route_count ? `${formatNumber(data.route_count)} live routes` : 'No routes discovered yet');
    } catch (_) {
      $('#status-pill').classList.add('offline');
      setText('#status', 'Unable to read status');
    }
  }

  async function loadUpdates() {
    try {
      const payload = await fetchJSON('/api/updates');
      const build = payload.build || {}; const settings = payload.settings || {}; const stateUpdate = payload.state || {};
      const enabled = $('#updates-enabled'); if (enabled) enabled.checked = Boolean(settings.enabled);
      const channel = $('#updates-channel'); if (channel) channel.value = settings.channel || 'releases';
      const interval = $('#updates-interval'); if (interval) interval.value = String(settings.interval_seconds || 3600);
      setText('#update-current-version', build.version || 'Current version');
      const details = $('#update-build-details'); if (details) { details.replaceChildren(); [['Channel', build.channel], ['Commit', build.commit], ['Platform', `${build.os || ''}/${build.arch || ''}`], ['Built', build.built_at]].forEach(([label, value]) => { const row = document.createElement('div'); row.className = 'detail-row'; const key = document.createElement('span'); key.textContent = label; const val = document.createElement('strong'); val.textContent = value || '—'; row.append(key, val); details.append(row); }); }
      setText('#update-phase', stateUpdate.phase || 'Idle');
      const available = payload.available; const card = $('#update-available'); if (card) card.hidden = !available; if (available) setText('#update-available-version', `${available.version} · ${available.channel}`);
      const warning = $('#update-warning'); const failed = stateUpdate.phase === 'rolled_back' || stateUpdate.phase === 'needs_manual_recovery'; if (warning) { warning.hidden = !failed || Boolean(stateUpdate.warning_acknowledged_at); warning.textContent = failed ? `Update warning: ${stateUpdate.error || 'The new version could not start.'}` : ''; if (failed && !stateUpdate.warning_acknowledged_at) { const button = document.createElement('button'); button.className = 'quiet-button'; button.textContent = 'Dismiss'; button.onclick = async () => { await fetchJSON('/api/updates/warning/acknowledge', { method: 'POST' }); loadUpdates(); }; warning.append(button); } }
      const history = $('#update-history-body'); const empty = $('#update-history-empty'); if (history) { history.replaceChildren(); (payload.history || []).forEach((item) => { const row = document.createElement('tr'); appendTextCell(row, item.version || '—'); appendTextCell(row, item.channel || '—'); appendTextCell(row, item.outcome || '—'); appendTextCell(row, dateValue(item.at)); appendTextCell(row, item.error || '—'); history.append(row); }); if (empty) empty.hidden = (payload.history || []).length > 0; }
    } catch (_) { setText('#updates-feedback', 'Update information is unavailable.'); }
  }

  async function loadSummary() {
    try {
      state.summary = await fetchJSON('/api/stats/summary');
      const s = state.summary;
      const success = s.total_requests ? Math.round((s.succeeded_requests / s.total_requests) * 100) : 0;
      setText('#metric-total', s.total_requests ? `${formatNumber(s.total_requests)} · ${success}%` : '—');
      setTokenMetric('#metric-tokens', s.input_tokens, s.output_tokens, s.total_tokens, s.cached_read_tokens, s.reasoning_tokens);
      setText('#metric-token-note', `${cacheHitPercent(s)} cache hit`);
      setText('#metric-cost', formatUSD(s.actual_cost_pico_usd || s.estimated_cost_pico_usd));
      setText('#metric-saved', formatUSD(s.saved_cost_pico_usd));
      setText('#metric-saved-percent', s.saved_percent_bps == null ? 'No baseline' : `${discountPercent(s.saved_percent_bps)} saved`);
      setText('#metric-cost-note', `Actual · estimate ${formatUSD(s.estimated_cost_pico_usd)}`);
      setText('#models-saved', formatUSD(s.saved_cost_pico_usd));
      setText('#models-saved-percent', s.saved_percent_bps == null ? 'No baseline' : `${discountPercent(s.saved_percent_bps)} saved`);
      setText('#metric-success-note', s.total_requests ? `${formatNumber(s.succeeded_requests)} succeeded · ${formatNumber(s.failed_requests)} failed` : 'Waiting for traffic');
      renderRequestSummary();
      renderStatsOverview();
    } catch (_) { setText('#metric-total', '—'); }
  }

  async function loadRequests() {
    try { state.requests = (await fetchJSON('/api/requests?limit=500')).data || []; renderRecentRequests(); renderRequestTable(); renderRequestSummary(); } catch (_) { setText('#request-list', 'Unable to load request statistics'); }
  }
  async function loadModelStats() { try { state.modelStats = (await fetchJSON('/api/stats/models')).data || []; renderStatsOverview(); renderModelStats(); } catch (_) { setText('#model-stats-body', 'Unable to load model statistics'); } }
  async function loadProviderStats() { try { state.providerStats = (await fetchJSON('/api/stats/providers')).data || []; renderProviderStats(); } catch (_) { setText('#provider-stats-body', 'Unable to load provider statistics'); } }
  async function loadGroupStats() { try { state.groupStats = (await fetchJSON('/api/stats/groups')).data || []; renderGroupStats(); } catch (_) { setText('#group-stats-body', 'Unable to load group statistics'); } }
  async function loadSubscriptionStats() { try { const items = (await fetchJSON('/api/stats/subscriptions')).data || []; const body = $('#subscription-stats-body'); const empty = $('#subscription-stats-empty'); if (!body) return; body.replaceChildren(); items.forEach((item) => { const row = document.createElement('tr'); appendTextCell(row, `${item.label || item.provider} · ${formatUSD(item.fee_pico_usd)} / cycle`); appendTextCell(row, `${formatNumber((item.input_tokens || 0) + (item.output_tokens || 0))} tokens`); appendTextCell(row, `${formatUSD(item.effective_input_pico_usd_per_million)} / 1M · ${formatUSD(item.effective_output_pico_usd_per_million)} / 1M`); appendTextCell(row, `${formatUSD(item.observed_5h_min_pico_usd_per_million)} · ${formatUSD(item.observed_5h_max_pico_usd_per_million)}`); body.append(row); }); if (empty) empty.hidden = items.length > 0; } catch (_) {} }

  function requestCost(request) { return request.actual_cost_pico_usd ?? request.estimated_cost_pico_usd ?? 0; }
  function renderRecentRequests() {
    const list = $('#request-list'); list.replaceChildren();
    state.requests.slice(0, 5).forEach((request) => {
      const row = document.createElement('button'); row.type = 'button'; row.className = 'request-row'; row.dataset.requestId = request.id;
      const dot = document.createElement('span'); dot.className = `provider-dot ${request.protocol === 'responses' ? 'surplus' : ''}`; row.append(dot);
      const main = document.createElement('span'); main.className = 'request-main'; const title = document.createElement('strong'); title.textContent = request.model; const detail = document.createElement('small'); detail.textContent = `${providerName(request.provider)} · ${protocolName(request.protocol)} · ${dateValue(request.received_at)}`; main.append(title, detail); row.append(main);
      const side = document.createElement('span'); side.className = 'request-side'; const cost = document.createElement('strong'); cost.textContent = formatUSD(requestCost(request)); const tokens = document.createElement('small'); tokens.textContent = `${formatNumber(request.total_tokens)} tokens`; side.append(cost, tokens); row.append(side); list.append(row);
    });
    if (!state.requests.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'No requests yet — your first call will appear here.'; list.append(empty); }
  }

  function renderRequestSummary() {
    const container = $('#request-summary'); if (!container) return; container.replaceChildren();
    const s = state.summary || {}; const items = [['TOTAL', formatNumber(s.total_requests)]];
    const tokenTotal = Number(s.input_tokens || 0) + Number(s.output_tokens || 0) + Number(s.total_tokens || 0) + Number(s.cached_read_tokens || 0) + Number(s.reasoning_tokens || 0);
    if (tokenTotal > 0) items.push(['TOKENS', null]);
    items.push(['ESTIMATED', formatUSD(s.estimated_cost_pico_usd)], ['ACTUAL', formatUSD(s.actual_cost_pico_usd)], ['SAVED', formatUSD(s.saved_cost_pico_usd)]);
    items.forEach(([label, value]) => { const item = document.createElement('div'); item.className = 'summary-item'; const title = document.createElement('span'); title.textContent = label; const number = document.createElement('strong'); if (label === 'TOKENS') number.append(tokenBreakdown(s.input_tokens, s.output_tokens, s.total_tokens, s.cached_read_tokens, s.reasoning_tokens)); else number.textContent = value; item.append(title, number); container.append(item); });
  }

  function makeMetricCard(label, value, note, accent) {
    const card = document.createElement('article'); card.className = `metric-card ${accent}`.trim();
    const title = document.createElement('span'); title.className = 'metric-label'; title.textContent = label;
    const number = document.createElement('strong'); number.textContent = value;
    const small = document.createElement('small'); small.textContent = note;
    card.append(title, number, small); return card;
  }

  function makeSpendMetricCard(summary) {
    const card = document.createElement('article'); card.className = 'metric-card accent-green spend-metric';
    const layout = document.createElement('div'); layout.className = 'spend-metric-layout';
    const actual = document.createElement('div'); const actualLabel = document.createElement('span'); actualLabel.className = 'metric-label'; actualLabel.textContent = 'ACTUAL SPEND'; const actualValue = document.createElement('strong'); actualValue.textContent = formatUSD(summary.actual_cost_pico_usd || summary.estimated_cost_pico_usd); actual.append(actualLabel, actualValue);
    const saved = document.createElement('div'); saved.className = 'saved-value'; const savedLabel = document.createElement('span'); savedLabel.className = 'metric-label'; savedLabel.textContent = 'SAVED'; const savedValue = document.createElement('strong'); savedValue.textContent = formatUSD(summary.saved_cost_pico_usd); const savedPercent = document.createElement('span'); savedPercent.className = 'saved-percent'; savedPercent.textContent = summary.saved_percent_bps == null ? 'No baseline' : `${discountPercent(summary.saved_percent_bps)} saved`; saved.append(savedLabel, savedValue, savedPercent);
    layout.append(actual, saved); const note = document.createElement('small'); note.textContent = `Actual · estimate ${formatUSD(summary.estimated_cost_pico_usd)}`; card.append(layout, note); return card;
  }

  function renderStatsOverview() {
    const container = $('#stats-overview'); if (!container) return; container.replaceChildren(); const s = state.summary || {};
    const success = s.total_requests ? Math.round((Number(s.succeeded_requests || 0) / Number(s.total_requests)) * 100) : 0;
    const retried = Number(s.retried_requests || 0);
    const items = [['REQUESTS · RELIABILITY', s.total_requests ? `${formatNumber(s.total_requests)} · ${success}%` : '—', `${formatNumber(s.total_attempts || 0)} attempts · ${formatNumber(retried)} retried`, 'accent-peach'], ['RESPONSE TIME', formatDuration(s.average_response_ms), `${formatDuration(s.fastest_response_ms)} fastest · ${formatDuration(s.slowest_response_ms)} slowest`, 'accent-blue']];
    items.forEach(([label, value, note, accent]) => container.append(makeMetricCard(label, value, note, accent)));
    const tokenCard = makeMetricCard('TOKENS', '', `${cacheHitPercent(s)} cache hit`, 'accent-lime'); tokenCard.querySelector('strong').append(tokenBreakdown(s.input_tokens, s.output_tokens, s.total_tokens, s.cached_read_tokens, s.reasoning_tokens)); container.append(tokenCard);
    container.append(makeSpendMetricCard(s));
  }

  function renderModelStats() {
    const body = $('#model-stats-body'); const empty = $('#model-stats-empty'); if (!body) return; body.replaceChildren(); const items = [...state.modelStats].sort((a, b) => Number(b.free) - Number(a.free) || Number(b.requests || 0) - Number(a.requests || 0) || String(a.model).localeCompare(String(b.model)));
    items.forEach((item) => { const row = document.createElement('tr'); const model = appendTextCell(row, ''); const strong = document.createElement('strong'); strong.textContent = item.model; const small = document.createElement('small'); small.textContent = item.free ? 'Free route available' : 'Priced route'; model.append(strong, small); if (item.free) { const badge = document.createElement('span'); badge.className = 'badge free'; badge.textContent = 'FREE'; model.append(document.createTextNode(' '), badge); } appendTextCell(row, `${formatNumber(item.requests)} · ${formatNumber(item.total_attempts)} attempts`); const success = appendTextCell(row, ''); const successStrong = document.createElement('strong'); successStrong.textContent = `${Math.round(Number(item.success_rate_bps || 0) / 100)}%`; const successSmall = document.createElement('small'); successSmall.textContent = `${formatNumber(item.succeeded_requests || 0)} succeeded`; success.append(successStrong, successSmall); const retries = appendTextCell(row, ''); const retryStrong = document.createElement('strong'); retryStrong.textContent = formatNumber(item.retried_requests || 0); const retrySmall = document.createElement('small'); retrySmall.textContent = `${Math.round(Number(item.retry_rate_bps || 0) / 100)}% of requests`; retries.append(retryStrong, retrySmall); appendTextCell(row, `${formatDuration(item.fastest_response_ms)} · ${formatDuration(item.average_response_ms)} avg · ${formatDuration(item.slowest_response_ms)}`); const tokens = appendTextCell(row, ''); tokens.append(tokenBreakdown(item.input_tokens, item.output_tokens, item.total_tokens, item.cached_read_tokens, item.reasoning_tokens)); const cost = appendTextCell(row, ''); const costStrong = document.createElement('strong'); costStrong.textContent = formatUSD(item.actual_cost_pico_usd); const costSmall = document.createElement('small'); costSmall.textContent = `est ${formatUSD(item.estimated_cost_pico_usd)} · saved ${formatUSD(item.saved_cost_pico_usd)}`; cost.append(costStrong, costSmall); body.append(row); }); empty.hidden = items.length > 0;
  }

  function renderProviderStats() {
    const body = $('#provider-stats-body'); const empty = $('#provider-stats-empty'); if (!body) return; body.replaceChildren();
    const items = [...state.providerStats].sort((a, b) => Number(b.requests || 0) - Number(a.requests || 0) || String(a.provider).localeCompare(String(b.provider)));
    items.forEach((item) => {
      const row = document.createElement('tr'); const provider = appendTextCell(row, ''); provider.append(providerBadge(item.provider));
      const requests = appendTextCell(row, ''); const requestsStrong = document.createElement('strong'); requestsStrong.textContent = formatNumber(item.requests); const requestsSmall = document.createElement('small'); requestsSmall.textContent = `${formatNumber(item.total_attempts)} attempts`; requests.append(requestsStrong, requestsSmall);
      const success = appendTextCell(row, ''); const successStrong = document.createElement('strong'); successStrong.textContent = `${Math.round(Number(item.success_rate_bps || 0) / 100)}%`; const successSmall = document.createElement('small'); successSmall.textContent = `${formatNumber(item.succeeded_requests || 0)} succeeded · ${formatNumber(item.failed_requests || 0)} failed`; success.append(successStrong, successSmall);
      const retries = appendTextCell(row, ''); const retryStrong = document.createElement('strong'); retryStrong.textContent = formatNumber(item.retried_requests || 0); const retrySmall = document.createElement('small'); retrySmall.textContent = `${Math.round(Number(item.retry_rate_bps || 0) / 100)}% of requests`; retries.append(retryStrong, retrySmall);
      appendTextCell(row, `${formatDuration(item.fastest_response_ms)} · ${formatDuration(item.average_response_ms)} avg · ${formatDuration(item.slowest_response_ms)}`);
      const tokens = appendTextCell(row, ''); tokens.append(tokenBreakdown(item.input_tokens, item.output_tokens, item.total_tokens, item.cached_read_tokens, item.reasoning_tokens));
      const spend = appendTextCell(row, ''); const spendStrong = document.createElement('strong'); spendStrong.textContent = formatUSD(item.actual_cost_pico_usd); const spendSmall = document.createElement('small'); spendSmall.textContent = `saved ${formatUSD(item.saved_cost_pico_usd)} · ${item.discount_percent_bps == null ? 'no baseline' : `${discountPercent(item.discount_percent_bps)} saved`} · est ${formatUSD(item.estimated_cost_pico_usd)}`; spend.append(spendStrong, spendSmall); body.append(row);
    });
    empty.hidden = items.length > 0;
  }

  function renderGroupStats() {
    const body = $('#group-stats-body'); const empty = $('#group-stats-empty'); if (!body) return; body.replaceChildren();
    const items = [...state.groupStats].sort((a, b) => Number(b.requests || 0) - Number(a.requests || 0) || String(a.group || a.slug).localeCompare(String(b.group || b.slug)));
    items.forEach((item) => {
      const row = document.createElement('tr'); const group = appendTextCell(row, ''); const strong = document.createElement('strong'); strong.textContent = item.group || item.slug || item.group_id; const small = document.createElement('small'); small.textContent = item.slug ? `/${item.slug}` : item.group_id; group.append(strong, small);
      const requests = appendTextCell(row, ''); const requestsStrong = document.createElement('strong'); requestsStrong.textContent = formatNumber(item.requests); const requestsSmall = document.createElement('small'); requestsSmall.textContent = `${formatNumber(item.total_attempts)} attempts`; requests.append(requestsStrong, requestsSmall);
      const success = appendTextCell(row, ''); const successStrong = document.createElement('strong'); successStrong.textContent = `${Math.round(Number(item.success_rate_bps || 0) / 100)}%`; const successSmall = document.createElement('small'); successSmall.textContent = `${formatNumber(item.succeeded_requests || 0)} succeeded · ${formatNumber(item.failed_requests || 0)} failed`; success.append(successStrong, successSmall);
      const retries = appendTextCell(row, ''); const retryStrong = document.createElement('strong'); retryStrong.textContent = formatNumber(item.retried_requests || 0); const retrySmall = document.createElement('small'); retrySmall.textContent = `${Math.round(Number(item.retry_rate_bps || 0) / 100)}% of requests`; retries.append(retryStrong, retrySmall);
      appendTextCell(row, `${formatDuration(item.fastest_response_ms)} · ${formatDuration(item.average_response_ms)} avg · ${formatDuration(item.slowest_response_ms)}`);
      const tokens = appendTextCell(row, ''); tokens.append(tokenBreakdown(item.input_tokens, item.output_tokens, item.total_tokens, item.cached_read_tokens, item.reasoning_tokens));
      const spend = appendTextCell(row, ''); const spendStrong = document.createElement('strong'); spendStrong.textContent = formatUSD(item.actual_cost_pico_usd); const spendSmall = document.createElement('small'); spendSmall.textContent = `saved ${formatUSD(item.saved_cost_pico_usd)} · ${item.discount_percent_bps == null ? 'no baseline' : `${discountPercent(item.discount_percent_bps)} saved`} · est ${formatUSD(item.estimated_cost_pico_usd)}`; spend.append(spendStrong, spendSmall); body.append(row);
    });
    empty.hidden = items.length > 0;
  }

  function filteredRequests() {
    const search = ($('#requests-search')?.value || '').trim().toLowerCase(); const filter = $('#requests-state')?.value || 'all';
    return state.requests.filter((request) => (filter === 'all' || request.state === filter) && (!search || `${request.id} ${request.model} ${request.provider} ${request.protocol}`.toLowerCase().includes(search)));
  }
  function renderRequestTable() {
    const body = $('#requests-table-body'); const empty = $('#requests-empty'); if (!body) return;
    const drawer = state.detailDrawer || $('#request-detail');
    if (drawer) { state.detailDrawer = drawer; drawer.hidden = true; }
    body.replaceChildren();
    filteredRequests().forEach((request) => {
      const row = document.createElement('tr'); row.className = 'data-row'; row.dataset.requestId = request.id; row.setAttribute('aria-expanded', 'false');
      const id = appendTextCell(row, ''); const idStrong = document.createElement('strong'); idStrong.textContent = shortID(request.id); const idSmall = document.createElement('small'); idSmall.textContent = dateValue(request.received_at); id.append(idStrong, idSmall);
      const model = appendTextCell(row, ''); const modelStrong = document.createElement('strong'); modelStrong.textContent = request.model; const modelSmall = document.createElement('small'); modelSmall.textContent = protocolName(request.protocol); model.append(modelStrong, modelSmall);
      const provider = appendTextCell(row, ''); provider.append(providerBadge(request.provider || 'unknown'));
      appendTextCell(row, `${formatNumber(request.attempts)} attempt${request.attempts === 1 ? '' : 's'}`);
      const tokens = appendTextCell(row, ''); tokens.append(tokenBreakdown(request.input_tokens, request.output_tokens, request.total_tokens, request.cached_read_tokens, request.reasoning_tokens));
      const cost = appendTextCell(row, ''); const costStrong = document.createElement('strong'); costStrong.textContent = formatUSD(requestCost(request)); const costSmall = document.createElement('small'); costSmall.textContent = request.actual_cost_pico_usd != null ? 'actual' : 'estimated'; cost.append(costStrong, costSmall);
      const discount = appendTextCell(row, ''); const discountValue = discountAmount(request); const discountStrong = document.createElement('strong'); discountStrong.textContent = discountValue == null ? '—' : discountValue === 0 ? '$0' : formatUSD(discountValue); const discountSmall = document.createElement('small'); discountSmall.textContent = discountValue == null ? discountUnavailableLabel(request) : discountValue === 0 ? '' : `${discountPercent(request.discount_percent_bps)} saved`; discount.append(discountStrong, discountSmall);
      appendTextCell(row, formatDuration(request.duration_ms)); const status = document.createElement('td'); status.append(stateBadge(request.state)); row.append(status); appendTextCell(row, dateValue(request.completed_at)); body.append(row);
    });
    empty.hidden = filteredRequests().length > 0;
  }

  function showRequestDetail(id) {
    const request = state.requests.find((item) => item.id === id); const body = $('#requests-table-body'); const drawer = state.detailDrawer || $('#request-detail'); if (!request || !body || !drawer) return;
    const selectedRow = body.querySelector(`tr[data-request-id="${CSS.escape(id)}"]`);
    const existingDetail = body.querySelector('.request-detail-row');
    if (existingDetail?.dataset.requestDetailFor === id) {
      existingDetail.remove();
      drawer.hidden = true;
      selectedRow?.setAttribute('aria-expanded', 'false');
      return;
    }
    existingDetail?.previousElementSibling?.setAttribute('aria-expanded', 'false');
    existingDetail?.remove();
    state.detailDrawer = drawer;
    drawer.hidden = false; drawer.replaceChildren(); const heading = document.createElement('h4'); heading.textContent = `Request ${shortID(request.id)}`; drawer.append(heading);
    const grid = document.createElement('div'); grid.className = 'detail-grid'; const values = [['Model', request.model], ['Provider', providerName(request.provider)], ['Upstream model', request.upstream_model || '—'], ['Attempts', formatNumber(request.attempts)], ['Protocol', protocolName(request.protocol)], ['State', request.state], ['Input tokens', formatNumber(request.input_tokens)], ['Output tokens', formatNumber(request.output_tokens)], ['Cached read', formatNumber(request.cached_read_tokens)], ['Cache write', formatNumber(request.cache_write_tokens)], ['Reasoning', formatNumber(request.reasoning_tokens)], ['Response time', formatDuration(request.duration_ms)], ['Estimated route cost', formatUSD(request.estimated_cost_pico_usd)], ['Official cost', request.official_cost_pico_usd ? formatUSD(request.official_cost_pico_usd) : 'Not available'], ['Actual cost', request.actual_cost_pico_usd != null ? formatUSD(request.actual_cost_pico_usd) : 'Not reported'], ['Discount', discountLabel(request)], ['Received', dateValue(request.received_at)], ['Completed', dateValue(request.completed_at)]];
    values.forEach(([label, value]) => { const cell = document.createElement('div'); const name = document.createElement('span'); name.textContent = label; const data = document.createElement('strong'); data.textContent = value; cell.append(name, data); grid.append(cell); });
    if (request.error_code) { const error = document.createElement('p'); error.className = 'modal-note'; error.textContent = `Terminal error: ${request.error_code}`; drawer.append(error); } drawer.append(grid);
    const attempts = document.createElement('div'); attempts.className = 'attempt-list'; const attemptsHeading = document.createElement('h5'); attemptsHeading.textContent = `Routing attempts (${formatNumber(request.attempts)})`; attempts.append(attemptsHeading);
    (request.attempt_details || []).forEach((attempt) => {
      const row = document.createElement('div'); row.className = 'attempt-row';
      const number = document.createElement('span'); number.className = 'attempt-number'; number.textContent = `#${attempt.number}`;
      const main = document.createElement('span'); main.className = 'attempt-main';
      const title = document.createElement('strong'); title.textContent = `${providerName(attempt.provider)} · ${attempt.upstream_model || '—'}`;
      const attemptDuration = formatDuration(attempt.duration_ms);
      const detail = document.createElement('small'); detail.textContent = attempt.error_class ? `${attempt.state} · ${attemptDuration} · ${attempt.error_class}: ${attempt.error_message || 'Provider error'}` : `${attempt.state} · ${attemptDuration} · ${dateValue(attempt.completed_at || attempt.started_at)}`;
      main.append(title, detail);
      row.append(number, main, stateBadge(attempt.state));
      if (attempt.raw_error) {
        const rawToggle = document.createElement('button'); rawToggle.type = 'button'; rawToggle.className = 'raw-error-toggle'; rawToggle.textContent = 'Show raw';
        const raw = document.createElement('pre'); raw.className = 'raw-error'; raw.hidden = true; raw.textContent = attempt.raw_error;
        rawToggle.addEventListener('click', () => { raw.hidden = !raw.hidden; rawToggle.textContent = raw.hidden ? 'Show raw' : 'Hide raw'; });
        main.append(rawToggle, raw);
      }
      attempts.append(row);
    });
    if (!request.attempt_details?.length) { const emptyAttempts = document.createElement('p'); emptyAttempts.className = 'attempt-empty'; emptyAttempts.textContent = 'No provider attempts were recorded for this legacy request.'; attempts.append(emptyAttempts); }
    drawer.append(attempts);
    if (!selectedRow) { drawer.hidden = true; return; }
    selectedRow.setAttribute('aria-expanded', 'true');
    const detailRow = document.createElement('tr'); detailRow.className = 'request-detail-row'; detailRow.dataset.requestDetailFor = id; const detailCell = document.createElement('td'); detailCell.colSpan = 10; detailCell.append(drawer); detailRow.append(detailCell); selectedRow.after(detailRow);
  }

  async function loadModels() {
    try { state.models = (await fetchJSON('/api/models')).data || []; pruneModelFilters(); renderModels(); renderRouteSummary(); } catch (_) { setText('#models-table-body', 'Unable to load model catalog'); }
  }
  function uniqueSorted(values) { return [...new Set(values.filter(Boolean))].sort((a, b) => String(a).localeCompare(String(b))); }
  function groupedModels() {
    const groups = new Map();
    state.models.forEach((route) => {
      const key = route.model || route.upstream_model || 'unknown';
      if (!groups.has(key)) groups.set(key, { model: key, routes: [], providers: [], tags: [], input_modalities: [], output_modalities: [], context_length: 0, free: false });
      const group = groups.get(key); group.routes.push(route); group.providers.push(route.provider); group.tags.push(...(route.tags || [])); group.input_modalities.push(...(route.input_modalities || [])); group.output_modalities.push(...(route.output_modalities || [])); group.context_length = Math.max(group.context_length, Number(route.context_length || 0)); group.free = group.free || Boolean(route.free);
    });
    return [...groups.values()].map((group) => summarizeModel(group, group.routes));
  }
  function summarizeModel(model, routes) {
    return { ...model, routes: routes.slice().sort((a, b) => providerName(a.provider).localeCompare(providerName(b.provider)) || String(a.upstream_model || '').localeCompare(String(b.upstream_model || ''))), providers: uniqueSorted(routes.map((route) => route.provider)), tags: uniqueSorted(routes.flatMap((route) => route.tags || [])), input_modalities: uniqueSorted(routes.flatMap((route) => route.input_modalities || [])), output_modalities: uniqueSorted(routes.flatMap((route) => route.output_modalities || [])), context_length: Math.max(...routes.map((route) => Number(route.context_length || 0)), 0), free: routes.some((route) => Boolean(route.free)) };
  }
  function filterValues(key) {
    if (key === 'model') return uniqueSorted(groupedModels().map((model) => model.model));
    if (key === 'provider') return uniqueSorted(state.models.map((model) => model.provider));
    if (key === 'tags') return uniqueSorted(state.models.flatMap((model) => model.tags || []));
    if (key === 'input') return uniqueSorted(state.models.flatMap((model) => model.input_modalities || []));
    if (key === 'output') return uniqueSorted(state.models.flatMap((model) => model.output_modalities || []));
    return [];
  }
  function pruneModelFilters() { Object.keys(state.modelFilters).forEach((key) => { const available = new Set(filterValues(key)); state.modelFilters[key] = state.modelFilters[key].filter((value) => available.has(value)); }); updateModelFilterIndicators(); }
  function routeMatchesFilters(route) {
    const filters = state.modelFilters;
    return (!filters.provider.length || filters.provider.includes(route.provider)) && (!filters.tags.length || filters.tags.some((value) => (route.tags || []).includes(value))) && (!filters.input.length || filters.input.some((value) => (route.input_modalities || []).includes(value))) && (!filters.output.length || filters.output.some((value) => (route.output_modalities || []).includes(value)));
  }
  function filteredModels() {
    const search = ($('#models-search')?.value || '').trim().toLowerCase();
    const items = groupedModels().map((model) => { if (state.modelFilters.model.length && !state.modelFilters.model.includes(model.model)) return null; const routes = model.routes.filter(routeMatchesFilters); if (!routes.length) return null; const visible = summarizeModel(model, routes); return (!search || `${visible.model} ${visible.providers.map(providerName).join(' ')} ${(visible.tags || []).join(' ')} ${(visible.input_modalities || []).join(' ')} ${(visible.output_modalities || []).join(' ')}`.toLowerCase().includes(search)) ? visible : null; }).filter(Boolean);
    const { key, direction } = state.modelSort; if (!key || !direction) return items;
    const price = (model, name) => { const values = model.routes.map((route) => Number(route.pricing?.[name])).filter((value, index) => model.routes[index].price_available && Number.isFinite(value)); return values.length ? Math.min(...values) : null; };
    const valueForSort = (model) => {
      if (key === 'model') return String(model.model || '').toLowerCase();
      if (key === 'provider') return model.providers.map(providerName).join(' · ').toLowerCase();
      if (key === 'modalities') return `${(model.input_modalities || []).join(' + ')} -> ${(model.output_modalities || []).join(' + ')}`.toLowerCase();
      if (key === 'tags') return (model.tags || []).join(' · ').toLowerCase();
      if (key === 'input') return price(model, 'input');
      if (key === 'discount') { const values = model.routes.map((route) => route.discount_percent_bps == null ? null : Number(route.discount_percent_bps)).filter((value) => value != null); return values.length ? Math.max(...values) : null; }
      if (key === 'context') return model.context_length == null ? null : Number(model.context_length);
      return null;
    };
    const compare = (a, b) => { const av = valueForSort(a); const bv = valueForSort(b); if (av == null && bv == null) return String(a.model).localeCompare(String(b.model)); if (av == null) return 1; if (bv == null) return -1; const result = typeof av === 'string' ? av.localeCompare(bv) : av - bv; return direction === 'desc' ? -result : result; };
    return items.sort(compare);
  }
  function updateModelSortIndicators() { $$('.table-sort').forEach((button) => { const active = state.modelSort.key === button.dataset.sortKey && state.modelSort.direction; const indicator = button.querySelector('.sort-indicator'); if (indicator) indicator.textContent = active ? (state.modelSort.direction === 'asc' ? '↑' : '↓') : '↕'; button.setAttribute('aria-sort', active ? (state.modelSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'); }); }
  function cycleModelSort(key) { if (state.modelSort.key !== key) state.modelSort = { key, direction: 'asc' }; else if (state.modelSort.direction === 'asc') state.modelSort.direction = 'desc'; else state.modelSort = { key: null, direction: null }; updateModelSortIndicators(); renderModels(); }
  function filterDisplayValue(key, value) { return key === 'provider' ? providerName(value) : String(value).replace(/_/g, ' '); }
  function closeModelFilter() { if (!state.filterPopover) return; state.filterPopover.remove(); state.filterPopover = null; }
  function renderFilterOptions(key) {
    const popover = state.filterPopover; if (!popover) return; const options = $('#models-filter-options'); options.replaceChildren();
    const sections = key === 'modalities' ? [['input', 'Input modalities'], ['output', 'Output modalities']] : [[key, '']]; const search = (popover.querySelector('.filter-option-search')?.value || '').trim().toLowerCase();
    sections.forEach(([sectionKey, heading]) => { const selected = new Set(key === 'modalities' ? (state.filterDraft?.[sectionKey] || []) : (state.filterDraft || [])); const values = filterValues(sectionKey); if (heading) { const title = document.createElement('h5'); title.className = 'filter-section-title'; title.textContent = heading; options.append(title); } const matching = values.filter((value) => filterDisplayValue(sectionKey, value).toLowerCase().includes(search)); matching.forEach((value) => { const label = document.createElement('label'); label.className = 'filter-option'; const input = document.createElement('input'); input.type = 'checkbox'; input.value = value; input.dataset.filterSection = sectionKey; input.checked = selected.has(value); const text = document.createElement('span'); text.textContent = filterDisplayValue(sectionKey, value); label.append(input, text); options.append(label); }); if (!matching.length) { const empty = document.createElement('p'); empty.className = 'filter-empty'; empty.textContent = 'No matching values'; options.append(empty); } });
  }
  function openModelFilter(key, button) {
    closeModelFilter(); const popover = document.createElement('div'); popover.id = 'models-filter-popover'; popover.className = 'filter-popover'; popover.setAttribute('role', 'dialog'); const title = key === 'modalities' ? 'input and output modalities' : key === 'tags' ? 'tags' : `${key}s`; popover.innerHTML = `<div class="filter-popover-heading"><strong>Filter ${title}</strong><button type="button" class="filter-close" aria-label="Close filter">×</button></div><input class="filter-option-search" type="search" placeholder="Search values…" autocomplete="off"><div class="filter-option-actions"><button type="button" data-filter-select-all>Select all</button><button type="button" data-filter-clear>Clear</button></div><div id="models-filter-options" class="filter-options"></div><div class="filter-popover-actions"><button type="button" class="quiet-button" data-filter-cancel>Cancel</button><button type="button" class="primary-button" data-filter-apply>Apply</button></div>`;
    document.body.append(popover); state.filterPopover = popover; state.filterDraft = key === 'modalities' ? { input: [...state.modelFilters.input], output: [...state.modelFilters.output] } : [...state.modelFilters[key]]; state.filterKey = key; popover.querySelector('.filter-option-search').addEventListener('input', () => renderFilterOptions(key)); popover.querySelector('.filter-close').addEventListener('click', closeModelFilter); popover.querySelector('[data-filter-cancel]').addEventListener('click', closeModelFilter); popover.querySelector('[data-filter-select-all]').addEventListener('click', () => { state.filterDraft = key === 'modalities' ? { input: filterValues('input'), output: filterValues('output') } : filterValues(key); renderFilterOptions(key); }); popover.querySelector('[data-filter-clear]').addEventListener('click', () => { state.filterDraft = key === 'modalities' ? { input: [], output: [] } : []; renderFilterOptions(key); }); popover.querySelector('[data-filter-apply]').addEventListener('click', () => { if (key === 'modalities') { state.modelFilters.input = [...popover.querySelectorAll('.filter-option input[data-filter-section="input"]:checked')].map((input) => input.value); state.modelFilters.output = [...popover.querySelectorAll('.filter-option input[data-filter-section="output"]:checked')].map((input) => input.value); } else state.modelFilters[key] = [...popover.querySelectorAll('.filter-option input:checked')].map((input) => input.value); closeModelFilter(); updateModelFilterIndicators(); renderModels(); });
    renderFilterOptions(key); popover.hidden = false; const rect = button.getBoundingClientRect(); const left = Math.max(12, Math.min(rect.left, window.innerWidth - 330)); popover.style.left = `${left}px`; popover.style.top = `${Math.min(rect.bottom + 8, window.innerHeight - 420)}px`;
  }
  function updateModelFilterIndicators() { $$('.table-filter').forEach((button) => { const key = button.dataset.filterKey; const count = key === 'modalities' ? state.modelFilters.input.length + state.modelFilters.output.length : state.modelFilters[key]?.length || 0; button.classList.toggle('active', count > 0); const badge = button.querySelector('.filter-count'); if (badge) { badge.textContent = count || ''; badge.hidden = count === 0; } }); const clear = $('#models-clear-filters'); if (clear) clear.disabled = !Object.values(state.modelFilters).some((values) => values.length); }
  function clearModelFilters() { Object.keys(state.modelFilters).forEach((key) => { state.modelFilters[key] = []; }); closeModelFilter(); updateModelFilterIndicators(); renderModels(); }
  function appendCompactPrice(container, route) {
    const line = document.createElement('div'); line.className = 'compact-price-line'; const part = (key, label) => { const wrapper = document.createElement('span'); wrapper.className = 'compact-price-part'; const name = document.createElement('small'); name.textContent = label; const current = Number(route.pricing?.[key] || 0); const official = Number(route.official_pricing?.[key] || 0); if (!route.price_available) { wrapper.append(name, document.createTextNode('—')); return wrapper; } if (official > current) { const original = document.createElement('s'); original.textContent = formatUSDPerMillion(official); const arrow = document.createElement('span'); arrow.className = 'price-arrow'; arrow.textContent = '→'; const discounted = document.createElement('strong'); discounted.textContent = formatUSDPerMillion(current); wrapper.append(name, original, arrow, discounted); } else { const value = document.createElement('strong'); value.textContent = formatUSDPerMillion(current); wrapper.append(name, value); } return wrapper; }; line.append(part('input', 'in'), part('output', 'out')); const discount = document.createElement('span'); discount.className = 'compact-discount'; discount.textContent = route.discount_percent_bps == null ? '—' : Number(route.discount_percent_bps) > 0 ? `-${discountPercent(route.discount_percent_bps)}` : '0%'; line.append(discount); container.append(line);
  }
  function renderModels() {
    const body = $('#models-table-body'); const empty = $('#models-empty'); if (!body) return; const models = filteredModels(); body.replaceChildren(); setText('#catalog-count', `${formatNumber(models.length)} model${models.length === 1 ? '' : 's'}`); setText('#catalog-free-count', `${formatNumber(state.models.length)} routes`); setText('#catalog-free-note', `${formatNumber(state.models.filter((model) => model.free).length)} free routes`); setText('#models-saved', formatUSD(state.summary?.saved_cost_pico_usd)); setText('#models-saved-percent', state.summary?.saved_percent_bps == null ? 'No baseline' : `${discountPercent(state.summary.saved_percent_bps)} saved`);
    models.forEach((model) => { const row = document.createElement('tr'); const route = appendTextCell(row, ''); const strong = document.createElement('strong'); strong.textContent = model.model; const small = document.createElement('small'); small.textContent = `${formatNumber(model.routes.length)} provider route${model.routes.length === 1 ? '' : 's'}`; route.append(strong, small); if (model.free) { const badge = document.createElement('span'); badge.className = 'badge free'; badge.textContent = 'FREE'; route.append(document.createTextNode(' '), badge); }
      const provider = appendTextCell(row, ''); provider.className = 'provider-lines'; model.routes.forEach((routeModel) => { const line = document.createElement('div'); line.className = 'provider-line'; line.append(providerBadge(routeModel.provider)); if (routeModel.free) { const badge = document.createElement('span'); badge.className = 'badge free'; badge.textContent = 'FREE'; line.append(badge); } provider.append(line); });
      const modalities = appendTextCell(row, ''); modalities.append(modalityDisplay(model.input_modalities, model.output_modalities)); modalities.className = 'modality-cell'; const tags = appendTextCell(row, (model.tags || []).join(' · ') || '—'); tags.className = 'tag-cell'; const pricing = appendTextCell(row, ''); pricing.className = 'pricing-cell'; model.routes.forEach((routeModel) => appendCompactPrice(pricing, routeModel)); pricing.title = 'Input / output per 1M tokens; crossed-out values are reference prices'; const context = appendTextCell(row, model.context_length ? formatCompact(model.context_length) : '—'); context.title = model.context_length ? `${formatNumber(model.context_length)} tokens` : ''; body.append(row); }); empty.hidden = models.length > 0; updateModelSortIndicators(); updateModelFilterIndicators();
  }
  function renderRouteSummary() { const container = $('#route-summary'); if (!container) return; container.replaceChildren(); const counts = {}; state.models.forEach((model) => { counts[model.provider] = (counts[model.provider] || 0) + 1; }); Object.entries(counts).forEach(([provider, count]) => { const row = document.createElement('div'); row.className = 'route-row'; const dot = document.createElement('span'); dot.className = `provider-dot ${provider === 'surplus' ? 'surplus' : ''}`; row.append(dot); const main = document.createElement('span'); main.className = 'route-main'; const strong = document.createElement('strong'); strong.textContent = providerName(provider); const small = document.createElement('small'); small.textContent = `${count} discovered route${count === 1 ? '' : 's'}`; main.append(strong, small); row.append(main); const badge = document.createElement('span'); badge.className = 'badge provider'; badge.textContent = `${count}`; row.append(badge); container.append(row); }); if (!Object.keys(counts).length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'Add a provider credential to discover routes.'; container.append(empty); } }

  async function loadKeys() { try { const payload = await fetchJSON('/api/client-keys'); const list = $('#key-list'); list.replaceChildren(); (payload.data || []).forEach((key) => { const row = document.createElement('div'); row.className = 'credential-row'; const icon = document.createElement('span'); icon.className = 'credential-icon'; icon.textContent = '⌘'; row.append(icon); const main = document.createElement('span'); main.className = 'credential-main'; const strong = document.createElement('strong'); strong.textContent = key.label; const small = document.createElement('small'); small.textContent = `${key.prefix} · created ${dateValue(key.created_at)}`; main.append(strong, small); row.append(main); const revoke = document.createElement('button'); revoke.type = 'button'; revoke.dataset.revokeKey = key.id; revoke.textContent = key.revoked_at ? 'Revoked' : 'Revoke'; revoke.disabled = Boolean(key.revoked_at); row.append(revoke); list.append(row); }); if (!payload.data?.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'No client keys yet.'; list.append(empty); } } catch (_) { setText('#key-list', 'Unable to load client keys'); } }
  let remotePollTimer;
  function remoteActionURL(value) { try { const parsed = new URL(value); return parsed.protocol === 'https:' && (parsed.hostname === 'tailscale.com' || parsed.hostname.endsWith('.tailscale.com')) ? parsed.href : ''; } catch (_) { return ''; } }
  function renderRemoteAccess(status) { const phase = status.phase || 'disabled'; const mode = status.desired_mode || 'disabled'; const transitional = ['starting', 'connecting', 'auth_required', 'stopping'].includes(phase); const phaseLabel = { disabled: 'Off', starting: 'Starting', connecting: 'Connecting', auth_required: 'Action required', online: 'Online', stopping: 'Stopping', error: 'Error' }[phase] || phase; setText('#remote-access-phase', phaseLabel); const modeControl = $('#remote-access-mode'); const host = $('#remote-access-hostname'); if (modeControl) modeControl.value = mode; if (host) { host.value = status.hostname || 'paylessforai'; host.disabled = mode !== 'disabled' || transitional || phase === 'online'; } setText('#remote-access-description', mode === 'funnel' ? 'The owner-only dashboard stays private to the authorizing Tailscale user. Only /v1 inference routes are public, and each request requires a PayLessForAI client key.' : mode === 'private' ? 'The dashboard and inference API are available to the authorizing Tailscale user on the tailnet. Inference still requires a PayLessForAI client key.' : 'Remote access is disabled. The local loopback server remains available at its usual address.'); const action = $('#remote-access-action'); const actionURL = remoteActionURL(status.action?.url); if (action) action.hidden = !actionURL; const link = $('#remote-access-action-link'); if (link) { link.href = actionURL || '#'; link.hidden = !actionURL; } const links = $('#remote-access-links'); if (links) links.hidden = phase !== 'online'; setText('#remote-access-dashboard', status.dashboard_url || ''); setText('#remote-access-base', status.base_url || ''); const error = $('#remote-access-error'); if (error) { error.hidden = !status.last_error; error.textContent = status.last_error?.message || ''; } const retry = $('#remote-access-retry'); if (retry) retry.hidden = phase !== 'error'; const stop = $('#remote-access-stop'); if (stop) stop.hidden = mode === 'disabled' || phase === 'disabled'; const forget = $('#remote-access-forget'); if (forget) forget.hidden = mode !== 'disabled' || transitional; const save = $('#remote-access-save'); if (save) save.disabled = transitional; if (transitional) { clearTimeout(remotePollTimer); remotePollTimer = setTimeout(loadRemoteAccess, 2000); } }
  async function loadRemoteAccess() { try { renderRemoteAccess(await fetchJSON('/api/remote-access')); } catch (_) { setText('#remote-access-error', 'Remote-access status is unavailable'); $('#remote-access-error').hidden = false; } }
  function setNetworkFeedback(message, kind = 'warning') { const feedback = $('#network-feedback'); if (!feedback) return; feedback.hidden = !message; feedback.className = `provider-feedback ${kind}`; feedback.textContent = message || ''; }
  async function loadNetworkSettings() { try { const data = await fetchJSON('/api/settings/network'); state.network = data; const active = data.active; const configured = data.configured?.port || active?.port || 9472; $('#network-port').value = configured; setText('#network-active', active ? `${active.host}:${active.port}${data.override_active ? ' · command-line override' : ''}` : 'Not active'); setText('#network-base-url', active?.base_url || 'Available after startup'); const status = data.restart_required ? `Saved for the next restart${data.override_active ? ' (remove -listen to use it)' : ''}.` : 'Active and saved setting match.'; setText('#network-status', status); setNetworkFeedback(''); } catch (error) { setNetworkFeedback(error.message, 'error'); } }
  function updateProviderFormMode() { ensureSubscriptionFields(); const custom = $('#provider-type')?.value === 'custom'; const fields = $('#custom-provider-fields'); const name = $('#provider-name'); const baseURL = $('#provider-base-url'); if (fields) fields.hidden = !custom; if (name) { name.required = custom; if (!custom) name.value = ''; } if (baseURL) { baseURL.required = custom; if (!custom) baseURL.value = ''; } const mode = $('#provider-access-mode')?.value || 'api'; const subscription = $('#subscription-fields'); if (subscription) subscription.hidden = mode !== 'subscription'; const fee = $('#subscription-fee'); if (fee) fee.required = mode === 'subscription'; }
  function setProviderFeedback(message, kind = 'warning') { const feedback = $('#provider-feedback'); if (!feedback) return; feedback.hidden = !message; feedback.className = `provider-feedback ${kind}`; feedback.textContent = message || ''; }
  function parseManualModels() { const raw = ($('#manual-models')?.value || '').split('\n').map((line) => line.trim()).filter(Boolean); const result = []; for (const line of raw) { const parts = line.split('|').map((part) => part.trim()); if (!parts[0]) throw new Error('Each manual model needs a model ID.'); const input = Number(parts[1] || 0); const output = Number(parts[2] || 0); if (!Number.isFinite(input) || !Number.isFinite(output) || input <= 0 || output <= 0) throw new Error(`Enter positive input and output prices for ${parts[0]}.`); result.push({ id: parts[0], input_price_pico_usd_per_token: Math.round(input * 1e6), output_price_pico_usd_per_token: Math.round(output * 1e6) }); } return result; }
  async function loadProviders() { try { const payload = await fetchJSON('/api/providers/credentials'); const list = $('#provider-list'); list.replaceChildren(); (payload.data || []).forEach((credential) => { const row = document.createElement('div'); row.className = 'credential-row'; const icon = document.createElement('span'); icon.className = `credential-icon ${credential.provider}`; icon.textContent = credential.provider === 'surplus' ? 'S' : credential.provider === 'openrouter' ? 'O' : 'P'; row.append(icon); const main = document.createElement('span'); main.className = 'credential-main'; const strong = document.createElement('strong'); strong.textContent = credential.label || providerName(credential.provider); const small = document.createElement('small'); small.textContent = `${providerName(credential.provider)} · ${credential.enabled ? 'Enabled' : 'Disabled'}${credential.base_url ? ` · ${credential.base_url}` : ''}`; main.append(strong, small); row.append(main); const remove = document.createElement('button'); remove.type = 'button'; remove.dataset.removeProvider = credential.id; remove.textContent = 'Remove'; row.append(remove); list.append(row); }); if (!payload.data?.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'No providers connected yet.'; list.append(empty); } } catch (_) { setText('#provider-list', 'Unable to load provider credentials'); } }

  function blankGroup() { return { name: '', slug: '', description: '', enabled: true, stages: [{ position: 0, name: 'Choose a route', sources: [], billing_classes: ['free', 'subscription', 'metered'], selection: 'lowest_expected_cost', try_retries: 1 }] }; }
  function renderGroups() { const list = $('#groups-list'); if (!list) return; const query = ($('#groups-search')?.value || '').toLowerCase().trim(); list.replaceChildren(); const groups = state.groups.filter((group) => !query || `${group.name} ${group.slug} ${JSON.stringify(group.stages)}`.toLowerCase().includes(query)); setText('#groups-count', formatNumber(state.groups.filter((group) => group.enabled).length)); setText('#groups-warning-count', formatNumber(state.groups.filter((group) => !group.enabled || !group.stages?.length).length)); groups.forEach((group) => { const row = document.createElement('div'); row.className = 'group-row'; const main = document.createElement('div'); main.className = 'group-main'; const title = document.createElement('strong'); title.textContent = group.name; const slug = document.createElement('code'); slug.textContent = group.slug; const note = document.createElement('small'); const count = group.stages?.length || 0; note.textContent = `${count} try${count === 1 ? '' : 'ies'} · ${group.enabled ? 'Enabled' : 'Disabled'}`; main.append(title, slug, note); row.append(main); const summary = document.createElement('span'); summary.className = 'group-summary'; summary.textContent = (group.stages || []).map((stage) => stage.name || 'Fallback').join(' → ') || 'Needs a try'; row.append(summary); const actions = document.createElement('div'); actions.className = 'group-actions'; const edit = document.createElement('button'); edit.type = 'button'; edit.className = 'quiet-button'; edit.dataset.editGroup = group.id; edit.textContent = 'Edit'; const copy = document.createElement('button'); copy.type = 'button'; copy.className = 'quiet-button'; copy.dataset.copyGroupSlug = group.slug; copy.textContent = 'Copy slug'; actions.append(edit, copy); row.append(actions); list.append(row); }); if (!groups.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = state.groups.length ? 'No groups match your search.' : 'Create a group alias for a routing strategy.'; list.append(empty); } }
  const renderGroupsOriginal = renderGroups;
  renderGroups = (...args) => { renderGroupsOriginal(...args); $$('#groups-list small').forEach((node) => { node.textContent = node.textContent.replace('tryies', 'tries'); }); };
  const usdPerMillionToPico = (value) => { if (String(value ?? '').trim() === '') return undefined; const parsed = Number(value); return Number.isFinite(parsed) && parsed >= 0 ? Math.round(parsed * 1e6) : undefined; };
  const picoPerTokenToUSDPerMillion = (value) => value == null ? '' : String(Number(value) / 1e6);
  const usdToPico = (value) => { if (String(value ?? '').trim() === '') return undefined; const parsed = Number(value); return Number.isFinite(parsed) && parsed >= 0 ? Math.round(parsed * 1e12) : undefined; };
  const picoToUSD = (value) => value == null ? '' : String(Number(value) / 1e12);
  function modelCandidates() { const groups = new Map(); state.models.forEach((route) => { const id = route.model || route.logical_model || ''; if (!id) return; if (!groups.has(id)) groups.set(id, { id, name: route.name || id, routes: [] }); groups.get(id).routes.push(route); }); return [...groups.values()].sort((a, b) => a.name.localeCompare(b.name)); }
  function routeBilling(route) { return route.billing_class || (route.free ? 'free' : route.provider === 'surplus' ? 'metered' : 'metered'); }
  function displayPrice(value) { return value == null ? '—' : formatUSDPerMillion(Number(value)); }
  function sourceRoutes(source) { return state.models.filter((route) => route.model === source.model_id && (!source.provider_name || route.provider === source.provider_name)); }
  function auctionRoute(source) { return sourceRoutes(source).find((route) => route.provider === 'surplus' && route.official_pricing); }
  function renderAuctionDetails(container, source) { const route = auctionRoute(source); if (!route) return; const percent = Number(source.maximum_official_price_percent ?? 100); const input = Math.round(Number(route.official_pricing.input || 0) * percent / 100); const output = Math.round(Number(route.official_pricing.output || 0) * percent / 100); const note = document.createElement('small'); note.className = 'auction-derived'; note.textContent = `Max ${percent}% of official · input ${displayPrice(input)} / 1M · output ${displayPrice(output)} / 1M (${100 - percent}% below)`; container.append(note); }
  function renderCandidates(card) { const list = card.querySelector('.source-candidates'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; const query = (card.querySelector('.source-search')?.value || '').trim().toLowerCase(); if (kind === 'group') { state.groups.filter((group) => group.id !== $('#group-id').value && `${group.name} ${group.slug}`.toLowerCase().includes(query)).forEach((group) => { const item = document.createElement('div'); item.className = 'source-candidate'; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = group.name; const small = document.createElement('small'); small.textContent = group.slug; main.append(strong, small); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addGroup = group.id; add.textContent = 'Add'; item.append(main, add); list.append(item); }); } else { modelCandidates().filter((model) => `${model.name} ${model.id} ${model.routes.map((route) => route.provider).join(' ')}`.toLowerCase().includes(query)).forEach((model) => { const item = document.createElement('div'); item.className = 'source-candidate'; item.dataset.modelId = model.id; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = model.name; const small = document.createElement('small'); small.textContent = model.id; main.append(strong, small); const routes = document.createElement('div'); routes.className = 'source-route-list'; model.routes.forEach((route) => { const chip = document.createElement('span'); chip.className = `source-route-chip ${routeBilling(route)}`; chip.textContent = `${providerName(route.provider)} · ${route.free ? 'FREE' : routeBilling(route).toUpperCase()} · ${displayPrice(route.pricing?.input)} / ${displayPrice(route.pricing?.output)}`; routes.append(chip); }); main.append(routes); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addModel = model.id; add.textContent = 'Add'; item.append(main, add); list.append(item); }); } if (!list.children.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = 'No matching models or groups.'; list.append(empty); } }
  function renderSelectedSources(card) { const list = card.querySelector('.selected-sources'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; card._sources = (card._sources || []).filter((source) => source.kind === kind); card._sources.forEach((source, sourceIndex) => { const row = document.createElement('div'); row.className = 'selected-source'; row.draggable = true; row.dataset.sourceIndex = sourceIndex; const handle = document.createElement('span'); handle.className = 'drag-handle'; handle.textContent = '☷'; const main = document.createElement('div'); main.className = 'selected-source-main'; const title = document.createElement('strong'); if (kind === 'group') { const group = state.groups.find((item) => item.id === source.group_id); title.textContent = group?.name || source.group_id || 'Choose a group'; } else { const model = modelCandidates().find((item) => item.id === source.model_id); title.textContent = model?.name || source.model_id || 'Choose a model'; } const meta = document.createElement('small'); meta.textContent = kind === 'group' ? (state.groups.find((item) => item.id === source.group_id)?.slug || '') : source.model_id; main.append(title, meta); if (kind === 'model') { const controls = document.createElement('div'); controls.className = 'selected-source-controls'; const providerLabel = document.createElement('label'); providerLabel.textContent = 'Provider'; const provider = document.createElement('select'); provider.className = 'source-provider'; const providers = [...new Set(sourceRoutes({ model_id: source.model_id }).map((route) => route.provider))]; provider.innerHTML = '<option value="">All providers</option>' + providers.map((value) => `<option value="${value}">${providerName(value)}</option>`).join(''); provider.value = source.provider_name || ''; providerLabel.append(provider); controls.append(providerLabel); if (source.provider_name === 'surplus' && auctionRoute(source)) { const auctionLabel = document.createElement('label'); auctionLabel.textContent = 'Max official %'; const auction = document.createElement('input'); auction.type = 'number'; auction.min = '0'; auction.max = '100'; auction.step = '1'; auction.className = 'source-auction-percent'; auction.value = source.maximum_official_price_percent ?? 100; auctionLabel.append(auction); controls.append(auctionLabel); renderAuctionDetails(main, source); } row.append(handle, main, controls); } else row.append(handle, main); const retryLabel = document.createElement('label'); retryLabel.textContent = 'Retries'; const retry = document.createElement('input'); retry.type = 'number'; retry.min = '0'; retry.max = '5'; retry.step = '1'; retry.className = 'source-retries'; retry.value = source.retries ?? 1; retryLabel.append(retry); const duplicate = document.createElement('button'); duplicate.type = 'button'; duplicate.className = 'source-action'; duplicate.textContent = 'Duplicate'; duplicate.dataset.duplicateSource = sourceIndex; const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'source-action'; remove.textContent = 'Remove'; remove.dataset.removeSource = sourceIndex; row.append(retryLabel, duplicate, remove); list.append(row); }); if (!list.children.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = kind === 'model' ? 'Add one or more models below.' : 'Add a group below.'; list.append(empty); } }
  function stageSourceOptions(stage, index) { const card = document.createElement('article'); card.className = 'group-stage-card'; card.dataset.stageIndex = index; const sources = (stage.sources || []).filter((source) => source.model_id || source.group_id).map((source) => ({ ...source, kind: source.kind || (source.group_id ? 'group' : 'model') })); const kind = sources[0]?.kind || 'model'; card._sources = sources; card.innerHTML = `<div class="stage-card-heading"><strong>TRY ${index + 1}</strong><button type="button" class="quiet-button remove-stage">Remove</button></div><label>Try name<input class="stage-name" maxlength="120"></label><div class="try-settings"><label>Retry this try<input class="try-retries" type="number" min="0" max="5" step="1"></label><small>Repeats the complete candidate block before moving to the next try.</small></div><label>Source type<select class="source-kind"><option value="model">Models</option><option value="group">Another group</option></select></label><div class="source-picker"><input class="source-search" type="search" placeholder="Search model name, provider, or group…" autocomplete="off"><div class="source-candidates"></div></div><div class="selected-sources"></div><label class="stage-billing">Access <span><label><input type="checkbox" value="free"> Free</label><label><input type="checkbox" value="subscription"> Subscription</label><label><input type="checkbox" value="metered"> Metered API</label></span></label><div class="stage-limit-grid"><label>Max input $ / 1M<input class="stage-input-limit" type="number" min="0" step="0.000001" placeholder="No limit"></label><label>Max output $ / 1M<input class="stage-output-limit" type="number" min="0" step="0.000001" placeholder="No limit"></label><label>Max expected $ / request<input class="stage-total-limit" type="number" min="0" step="0.000001" placeholder="No limit"></label></div><small class="stage-explanation">Candidates are tried in the order shown. Drag to reorder; duplicate a model to use another auction percentage.</small>`; card.querySelector('.stage-name').value = stage.name || ''; card.querySelector('.source-kind').value = kind; card.querySelectorAll('.stage-billing input').forEach((input) => { input.checked = (stage.billing_classes || ['free', 'subscription', 'metered']).includes(input.value); }); card.querySelector('.stage-input-limit').value = picoPerTokenToUSDPerMillion(stage.maximum_input_pico_usd_per_token); card.querySelector('.stage-output-limit').value = picoPerTokenToUSDPerMillion(stage.maximum_output_pico_usd_per_token); card.querySelector('.stage-total-limit').value = picoToUSD(stage.maximum_expected_cost_pico_usd); card.querySelector('.try-retries').value = stage.try_retries ?? stage.same_route_retries ?? 1; card.querySelector('.source-kind').addEventListener('change', () => { card._sources = []; renderSelectedSources(card); renderCandidates(card); previewCurrentGroup(); }); card.querySelector('.source-search').addEventListener('input', () => renderCandidates(card)); card.addEventListener('click', (event) => { const addModel = event.target.closest('[data-add-model]'); const addGroup = event.target.closest('[data-add-group]'); if (addModel) { card._sources.push({ kind: 'model', model_id: addModel.dataset.addModel, retries: 1 }); renderSelectedSources(card); renderCandidates(card); previewCurrentGroup(); } if (addGroup) { card._sources = [{ kind: 'group', group_id: addGroup.dataset.addGroup, retries: 1 }]; renderSelectedSources(card); renderCandidates(card); previewCurrentGroup(); } const duplicate = event.target.closest('[data-duplicate-source]'); if (duplicate) { card._sources.splice(Number(duplicate.dataset.duplicateSource) + 1, 0, { ...card._sources[Number(duplicate.dataset.duplicateSource)] }); renderSelectedSources(card); previewCurrentGroup(); } const remove = event.target.closest('[data-remove-source]'); if (remove) { card._sources.splice(Number(remove.dataset.removeSource), 1); renderSelectedSources(card); previewCurrentGroup(); } }); card.addEventListener('input', (event) => { if (event.target.classList.contains('source-retries')) card._sources[Number(event.target.closest('.selected-source').dataset.sourceIndex)].retries = Number(event.target.value || 0); if (event.target.classList.contains('source-auction-percent')) card._sources[Number(event.target.closest('.selected-source').dataset.sourceIndex)].maximum_official_price_percent = Number(event.target.value || 0); renderSelectedSources(card); }); card.addEventListener('change', (event) => { if (event.target.classList.contains('source-provider')) { card._sources[Number(event.target.closest('.selected-source').dataset.sourceIndex)].provider_name = event.target.value; renderSelectedSources(card); } }); card.addEventListener('dragstart', (event) => { const row = event.target.closest('.selected-source'); if (row) { event.dataTransfer.setData('text/plain', row.dataset.sourceIndex); row.classList.add('dragging'); } }); card.addEventListener('dragend', (event) => event.target.closest('.selected-source')?.classList.remove('dragging')); card.addEventListener('dragover', (event) => { if (event.target.closest('.selected-source')) event.preventDefault(); }); card.addEventListener('drop', (event) => { const target = event.target.closest('.selected-source'); if (!target) return; event.preventDefault(); const from = Number(event.dataTransfer.getData('text/plain')); const to = Number(target.dataset.sourceIndex); if (from === to || Number.isNaN(from) || Number.isNaN(to)) return; const moved = card._sources.splice(from, 1)[0]; card._sources.splice(to, 0, moved); renderSelectedSources(card); previewCurrentGroup(); }); renderSelectedSources(card); renderCandidates(card); return card; }
  function renderGroupStages(definition) { const container = $('#group-stage-list'); if (!container) return; container.replaceChildren(); const stages = definition.stages?.length ? definition.stages : blankGroup().stages; stages.forEach((stage, index) => container.append(stageSourceOptions(stage, index))); }
  function collectGroupDefinition() { const stages = $$('#group-stage-list .group-stage-card').map((card, index) => { const stage = { position: index, name: card.querySelector('.stage-name').value, sources: (card._sources || []).map((source) => ({ ...source })), billing_classes: [...card.querySelectorAll('.stage-billing input:checked')].map((input) => input.value), selection: 'lowest_expected_cost', try_retries: Number(card.querySelector('.try-retries').value || 0) }; const inputLimit = usdPerMillionToPico(card.querySelector('.stage-input-limit').value); const outputLimit = usdPerMillionToPico(card.querySelector('.stage-output-limit').value); const totalLimit = usdToPico(card.querySelector('.stage-total-limit').value); if (inputLimit !== undefined) stage.maximum_input_pico_usd_per_token = inputLimit; if (outputLimit !== undefined) stage.maximum_output_pico_usd_per_token = outputLimit; if (totalLimit !== undefined) stage.maximum_expected_cost_pico_usd = totalLimit; return stage; }); return { id: $('#group-id').value || undefined, revision: Number($('#group-revision').value || 0), name: $('#group-name').value, slug: $('#group-slug').value, description: $('#group-description').value, enabled: $('#group-enabled').checked, stages }; }

  // Focused group editor: search first, then choose individual provider routes.
  // The legacy renderer above is kept for persisted compatibility, but this
  // renderer is the only one used by the live editor.
  function routePriceTextV3(route) { if (!route) return 'No provider route discovered'; const currentInput = displayPrice(route.pricing?.input); const currentOutput = displayPrice(route.pricing?.output); if (route.provider === 'surplus' && route.official_pricing) { const discount = route.discount_percent_bps == null ? '' : ` · -${discountPercent(route.discount_percent_bps)} discount`; return `${providerName(route.provider)} · ${routeBilling(route).toUpperCase()} · ${currentInput} in / ${currentOutput} out${discount}`; } return `${providerName(route.provider)} · ${route.free ? 'FREE' : routeBilling(route).toUpperCase()} · ${currentInput} in / ${currentOutput} out`; }
  function renderCandidatesV3(card) { const list = card.querySelector('.source-candidates'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; const query = (card.querySelector('.source-search')?.value || '').trim().toLowerCase(); list.hidden = !query; if (!query) return; if (kind === 'group') { state.groups.filter((group) => group.id !== $('#group-id').value && `${group.name} ${group.slug}`.toLowerCase().includes(query)).forEach((group) => { const item = document.createElement('div'); item.className = 'source-candidate'; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = group.name; const small = document.createElement('small'); small.textContent = group.slug; main.append(strong, small); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addGroup = group.id; add.textContent = 'Add group'; item.append(main, add); list.append(item); }); } else { modelCandidates().filter((model) => `${model.name} ${model.id} ${model.routes.map((route) => route.provider).join(' ')}`.toLowerCase().includes(query)).forEach((model) => { const item = document.createElement('div'); item.className = 'source-candidate'; const selected = (card._sources || []).some((source) => source.kind === 'model' && source.model_id === model.id); if (selected) item.classList.add('selected'); item.dataset.modelId = model.id; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = model.name; const small = document.createElement('small'); small.textContent = model.id; main.append(strong, small); const routes = document.createElement('div'); routes.className = 'source-route-list'; model.routes.forEach((route) => { const chip = document.createElement('span'); chip.className = `source-route-chip ${routeBilling(route)}`; chip.textContent = routePriceTextV3(route); routes.append(chip); }); main.append(routes); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addModel = model.id; add.textContent = selected ? 'Add another set' : 'Add all routes'; item.append(main, add); list.append(item); }); } if (!list.children.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = 'No matching models or groups.'; list.append(empty); } }
  function renderSelectedSourcesV3(card) { const list = card.querySelector('.selected-sources'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; card._sources = (card._sources || []).filter((source) => source.kind === kind); if (!card._sources.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = kind === 'model' ? 'Selected provider routes will appear here.' : 'Selected group will appear here.'; list.append(empty); return; } card._sources.forEach((source, sourceIndex) => { const row = document.createElement('div'); row.className = 'selected-source'; row.draggable = true; row.dataset.sourceIndex = sourceIndex; const handle = document.createElement('span'); handle.className = 'drag-handle'; handle.textContent = '☷'; const main = document.createElement('div'); main.className = 'selected-source-main'; const model = kind === 'model' ? modelDisplay(source.model_id) : null; const title = document.createElement('strong'); title.textContent = kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group'); const meta = document.createElement('small'); meta.textContent = kind === 'model' ? `${source.model_id} · ${providerName(source.provider_name || 'provider')}` : (state.groups.find((group) => group.id === source.group_id)?.slug || ''); main.append(title, meta); const route = kind === 'model' ? routeForSource(source) : null; if (kind === 'model') { const price = document.createElement('small'); price.className = 'selected-route-price'; price.textContent = routePriceTextV3(route); main.append(price); } const controls = document.createElement('div'); controls.className = 'selected-source-controls'; if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) { const auctionLabel = document.createElement('label'); auctionLabel.textContent = 'Max discount %'; const auction = document.createElement('input'); auction.type = 'number'; auction.min = '0'; auction.max = '100'; auction.step = '1'; auction.className = 'source-auction-percent'; auction.value = 100 - Number(source.maximum_official_price_percent ?? 100); auctionLabel.append(auction); controls.append(auctionLabel); } const retryLabel = document.createElement('label'); retryLabel.textContent = 'Retries'; const retry = document.createElement('input'); retry.type = 'number'; retry.min = '0'; retry.max = '5'; retry.step = '1'; retry.className = 'source-retries'; retry.value = source.retries ?? 1; retryLabel.append(retry); controls.append(retryLabel); const duplicate = document.createElement('button'); duplicate.type = 'button'; duplicate.className = 'source-action'; duplicate.dataset.duplicateSource = sourceIndex; duplicate.textContent = 'Duplicate'; controls.append(duplicate); const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'source-action'; remove.dataset.removeSource = sourceIndex; remove.textContent = 'Deselect'; controls.append(remove); row.append(handle, main, controls); list.append(row); }); }
  function stageSourceOptionsV3(stage, index) { const card = document.createElement('article'); card.className = 'group-stage-card'; card.dataset.stageIndex = index; const rawSources = (stage.sources || []).filter((source) => source.model_id || source.group_id).map((source) => ({ ...source, kind: source.kind || (source.group_id ? 'group' : 'model') })); card._sources = expandProviderSources(rawSources); card._stagePolicy = { billing_classes: stage.billing_classes || ['free', 'subscription', 'metered'], maximum_input_pico_usd_per_token: stage.maximum_input_pico_usd_per_token, maximum_output_pico_usd_per_token: stage.maximum_output_pico_usd_per_token, maximum_expected_cost_pico_usd: stage.maximum_expected_cost_pico_usd }; const kind = card._sources[0]?.kind || 'model'; card.innerHTML = `<div class="stage-card-heading"><strong>TRY ${index + 1}</strong><button type="button" class="quiet-button remove-stage">Remove</button></div><label>Try name<input class="stage-name" maxlength="120"></label><div class="source-mode-row"><label>Find in<select class="source-kind"><option value="model">Models</option><option value="group">Groups</option></select></label><small>Search by name, provider, or model ID. Nothing is shown until you type.</small></div><div class="source-picker"><input class="source-search" type="search" placeholder="Type to search models or groups…" autocomplete="off"><div class="source-candidates" hidden></div></div><div class="selected-route-heading"><strong>Selected provider routes</strong><small>All routes are selected by default. Deselect or reorder individual provider routes.</small></div><div class="selected-sources"></div><div class="try-settings try-settings-bottom"><label>Retry whole try block<input class="try-retries" type="number" min="0" max="5" step="1"></label><small>After each route's retries are exhausted, repeat this entire block before moving to the next try.</small></div><small class="stage-explanation">Drag routes to reorder. Duplicate a route when you need a separate auction discount cap.</small>`; card.querySelector('.stage-name').value = stage.name || ''; card.querySelector('.source-kind').value = kind; card.querySelector('.try-retries').value = stage.try_retries ?? 1; card.querySelector('.source-kind').addEventListener('change', () => { card._sources = []; renderSelectedSourcesV3(card); renderCandidatesV3(card); previewCurrentGroup(); }); card.querySelector('.source-search').addEventListener('input', () => renderCandidatesV3(card)); card.addEventListener('click', (event) => { const addModel = event.target.closest('[data-add-model]'); const addGroup = event.target.closest('[data-add-group]'); if (addModel) { const providers = providerNamesForModel(addModel.dataset.addModel); providers.forEach((provider) => card._sources.push({ kind: 'model', model_id: addModel.dataset.addModel, provider_name: provider, retries: 1 })); if (!providers.length) card._sources.push({ kind: 'model', model_id: addModel.dataset.addModel, retries: 1 }); renderSelectedSourcesV3(card); renderCandidatesV3(card); previewCurrentGroup(); return; } if (addGroup) { card._sources = [{ kind: 'group', group_id: addGroup.dataset.addGroup, retries: 1 }]; renderSelectedSourcesV3(card); renderCandidatesV3(card); previewCurrentGroup(); return; } const duplicate = event.target.closest('[data-duplicate-source]'); if (duplicate) { const source = card._sources[Number(duplicate.dataset.duplicateSource)]; if (source) card._sources.splice(Number(duplicate.dataset.duplicateSource) + 1, 0, { ...source }); renderSelectedSourcesV3(card); previewCurrentGroup(); return; } const remove = event.target.closest('[data-remove-source]'); if (remove) { card._sources.splice(Number(remove.dataset.removeSource), 1); renderSelectedSourcesV3(card); renderCandidatesV3(card); previewCurrentGroup(); } }); card.addEventListener('input', (event) => { const row = event.target.closest('.selected-source'); if (!row) return; const source = card._sources[Number(row.dataset.sourceIndex)]; if (!source) return; if (event.target.classList.contains('source-retries')) source.retries = Number(event.target.value || 0); if (event.target.classList.contains('source-auction-percent')) source.maximum_official_price_percent = Math.max(0, Math.min(100, 100 - Number(event.target.value || 0))); renderSelectedSourcesV3(card); }); card.addEventListener('dragstart', (event) => { const row = event.target.closest('.selected-source'); if (row) { event.dataTransfer.setData('text/plain', row.dataset.sourceIndex); row.classList.add('dragging'); } }); card.addEventListener('dragend', (event) => event.target.closest('.selected-source')?.classList.remove('dragging')); card.addEventListener('dragover', (event) => { if (event.target.closest('.selected-source')) event.preventDefault(); }); card.addEventListener('drop', (event) => { const target = event.target.closest('.selected-source'); if (!target) return; event.preventDefault(); const from = Number(event.dataTransfer.getData('text/plain')); const to = Number(target.dataset.sourceIndex); if (from === to || Number.isNaN(from) || Number.isNaN(to)) return; const moved = card._sources.splice(from, 1)[0]; card._sources.splice(to, 0, moved); renderSelectedSourcesV3(card); previewCurrentGroup(); }); renderSelectedSourcesV3(card); renderCandidatesV3(card); return card; }
  function renderGroupStagesV3(definition) { const container = $('#group-stage-list'); if (!container) return; container.replaceChildren(); const stages = definition.stages?.length ? definition.stages : blankGroup().stages; stages.forEach((stage, index) => container.append(stageSourceOptionsV3(stage, index))); }
  function collectGroupDefinitionV3() { const stages = $$('#group-stage-list .group-stage-card').map((card, index) => { const policy = card._stagePolicy || {}; const stage = { position: index, name: card.querySelector('.stage-name').value, sources: (card._sources || []).map((source) => ({ ...source })), billing_classes: policy.billing_classes || ['free', 'subscription', 'metered'], selection: 'lowest_expected_cost', try_retries: Number(card.querySelector('.try-retries').value || 0) }; if (policy.maximum_input_pico_usd_per_token != null) stage.maximum_input_pico_usd_per_token = policy.maximum_input_pico_usd_per_token; if (policy.maximum_output_pico_usd_per_token != null) stage.maximum_output_pico_usd_per_token = policy.maximum_output_pico_usd_per_token; if (policy.maximum_expected_cost_pico_usd != null) stage.maximum_expected_cost_pico_usd = policy.maximum_expected_cost_pico_usd; return stage; }); return { id: $('#group-id').value || undefined, revision: Number($('#group-revision').value || 0), name: $('#group-name').value, slug: $('#group-slug').value, description: $('#group-description').value, enabled: $('#group-enabled').checked, stages }; }
  routePriceText = routePriceTextV3;
  renderGroupStages = renderGroupStagesV3;
  collectGroupDefinition = collectGroupDefinitionV3;

  // Final route-picker interaction model: a compact search trigger, an
  // overlay search result list, and route cards with separate pricing and
  // reliability controls.
  function routeAccessLabelV4(route) { return route?.free ? 'FREE' : routeBilling(route) === 'subscription' ? 'SUBSCRIPTION' : ''; }
  function routePriceTextV4(route) { if (!route) return 'No provider route discovered'; const access = routeAccessLabelV4(route); const discount = route.provider === 'surplus' && route.official_pricing && route.discount_percent_bps != null ? ` · -${discountPercent(route.discount_percent_bps)} discount` : ''; return `${providerName(route.provider)}${access ? ` · ${access}` : ''}${discount} · ${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`; }
  function appendRoutePriceV4(container, route) { const line = document.createElement('span'); line.className = 'route-price-line'; if (!route) { line.textContent = 'No provider route discovered'; container.append(line); return; } const access = routeAccessLabelV4(route); const discount = route.provider === 'surplus' && route.official_pricing && route.discount_percent_bps != null ? `-${discountPercent(route.discount_percent_bps)}` : ''; const provider = document.createElement('span'); provider.className = 'route-price-provider'; provider.textContent = providerName(route.provider); line.append(provider); if (access) { const accessNode = document.createElement('span'); accessNode.className = `route-access-label ${access.toLowerCase()}`; accessNode.textContent = access; line.append(accessNode); } if (discount) { const discountNode = document.createElement('strong'); discountNode.className = 'route-discount'; discountNode.textContent = discount; line.append(discountNode); } const price = document.createElement('span'); price.className = 'route-price-values'; price.textContent = `${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`; line.append(price); container.append(line); }
  function auctionCapV4(route, discount) { if (!route?.official_pricing) return null; const percent = Math.max(0, Math.min(100, 100 - Number(discount || 0))); return { input: Math.round(Number(route.official_pricing.input || 0) * percent / 100), output: Math.round(Number(route.official_pricing.output || 0) * percent / 100) }; }
  function renderAuctionPricingV4(row, source, route) { const pricing = row.querySelector('.auction-pricing'); if (!pricing || !route?.official_pricing) return; const discount = Math.max(0, Math.min(100, 100 - Number(source.maximum_official_price_percent ?? 100))); const cap = auctionCapV4(route, discount); const value = pricing.querySelector('.discount-value'); if (value) value.textContent = `-${discount}%`; const input = pricing.querySelector('.auction-cap-input'); const output = pricing.querySelector('.auction-cap-output'); if (input) input.textContent = displayPrice(cap.input); if (output) output.textContent = displayPrice(cap.output); }
  function closeSourcePickerV4(card) { const picker = card.querySelector('.source-search-popover'); const toggle = card.querySelector('.source-search-toggle'); const input = card.querySelector('.source-search'); const list = card.querySelector('.source-candidates'); if (input) input.value = ''; if (list) { list.replaceChildren(); list.hidden = true; } if (picker) picker.hidden = true; if (toggle) toggle.hidden = false; }
  function openSourcePickerV4(card) { const picker = card.querySelector('.source-search-popover'); const toggle = card.querySelector('.source-search-toggle'); const input = card.querySelector('.source-search'); if (picker) picker.hidden = false; if (toggle) toggle.hidden = true; input?.focus(); }
  function renderCandidatesV4(card) { const list = card.querySelector('.source-candidates'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; const query = (card.querySelector('.source-search')?.value || '').trim().toLowerCase(); list.hidden = !query; if (!query) return; if (kind === 'group') { state.groups.filter((group) => group.id !== $('#group-id').value && `${group.name} ${group.slug}`.toLowerCase().includes(query)).forEach((group) => { const item = document.createElement('div'); item.className = 'source-candidate'; item.dataset.groupId = group.id; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = group.name; const small = document.createElement('small'); small.textContent = group.slug; main.append(strong, small); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addGroup = group.id; add.textContent = 'Add group'; item.append(main, add); list.append(item); }); } else { modelCandidates().filter((model) => `${model.name} ${model.id} ${model.routes.map((route) => route.provider).join(' ')}`.toLowerCase().includes(query)).forEach((model) => { const item = document.createElement('div'); item.className = 'source-candidate'; item.dataset.modelId = model.id; const selected = (card._sources || []).some((source) => source.kind === 'model' && source.model_id === model.id); if (selected) item.classList.add('selected'); const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = model.name; const small = document.createElement('small'); small.textContent = model.id; main.append(strong, small); const routes = document.createElement('div'); routes.className = 'source-route-list'; model.routes.forEach((route) => { const chip = document.createElement('span'); chip.className = `source-route-chip ${routeBilling(route)}`; appendRoutePriceV4(chip, route); routes.append(chip); }); main.append(routes); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addModel = model.id; add.textContent = selected ? 'Add another set' : 'Add all routes'; item.append(main, add); list.append(item); }); } if (!list.children.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = 'No matching models or groups.'; list.append(empty); } }
  function renderSelectedSourcesV4(card) { const list = card.querySelector('.selected-sources'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; card._sources = (card._sources || []).filter((source) => source.kind === kind); if (!card._sources.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = kind === 'model' ? 'Selected provider routes will appear here.' : 'Selected group will appear here.'; list.append(empty); return; } card._sources.forEach((source, sourceIndex) => { const row = document.createElement('div'); row.className = 'selected-source'; row.draggable = true; row.dataset.sourceIndex = sourceIndex; const handle = document.createElement('span'); handle.className = 'drag-handle'; handle.textContent = '☷'; const main = document.createElement('div'); main.className = 'selected-source-main'; const model = kind === 'model' ? modelDisplay(source.model_id) : null; const title = document.createElement('strong'); title.textContent = kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group'); const provider = document.createElement('span'); provider.className = 'selected-route-provider'; provider.textContent = kind === 'model' ? providerName(source.provider_name || 'provider') : 'Group fallback'; const meta = document.createElement('small'); meta.textContent = kind === 'model' ? source.model_id : (state.groups.find((group) => group.id === source.group_id)?.slug || ''); main.append(title, provider, meta); const route = kind === 'model' ? routeForSource(source) : null; if (kind === 'model') appendRoutePriceV4(main, route); const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'source-remove'; remove.dataset.removeSource = sourceIndex; remove.textContent = 'Remove'; const controls = document.createElement('div'); controls.className = 'selected-source-controls'; if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) { const pricing = document.createElement('div'); pricing.className = 'route-setting-section auction-pricing'; const heading = document.createElement('div'); heading.className = 'route-setting-heading'; heading.innerHTML = '<strong>Pricing</strong><output class="discount-value"></output>'; pricing.append(heading); const slider = document.createElement('input'); slider.type = 'range'; slider.min = '0'; slider.max = '100'; slider.step = '1'; slider.className = 'source-auction-percent'; slider.value = 100 - Number(source.maximum_official_price_percent ?? 100); pricing.append(slider); const cap = document.createElement('small'); cap.className = 'auction-cap'; cap.innerHTML = 'Cap <strong class="auction-cap-input"></strong> in / <strong class="auction-cap-output"></strong> out'; pricing.append(cap); controls.append(pricing); } const reliability = document.createElement('div'); reliability.className = 'route-setting-section route-reliability'; const retryHeading = document.createElement('strong'); retryHeading.className = 'route-setting-heading'; retryHeading.textContent = 'Reliability'; const retryLabel = document.createElement('label'); retryLabel.textContent = 'Retries'; const retry = document.createElement('input'); retry.type = 'number'; retry.min = '0'; retry.max = '5'; retry.step = '1'; retry.className = 'source-retries'; retry.value = source.retries ?? 1; retryLabel.append(retry); const duplicate = document.createElement('button'); duplicate.type = 'button'; duplicate.className = 'source-action'; duplicate.dataset.duplicateSource = sourceIndex; duplicate.textContent = 'Duplicate'; reliability.append(retryHeading, retryLabel, duplicate); controls.append(reliability); row.append(handle, main, remove, controls); list.append(row); if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) renderAuctionPricingV4(row, source, route); }); }
  function stageSourceOptionsV4(stage, index) { const card = document.createElement('article'); card.className = 'group-stage-card'; card.dataset.stageIndex = index; const rawSources = (stage.sources || []).filter((source) => source.model_id || source.group_id).map((source) => ({ ...source, kind: source.kind || (source.group_id ? 'group' : 'model') })); card._sources = expandProviderSources(rawSources); card._stagePolicy = { billing_classes: stage.billing_classes || ['free', 'subscription', 'metered'], maximum_input_pico_usd_per_token: stage.maximum_input_pico_usd_per_token, maximum_output_pico_usd_per_token: stage.maximum_output_pico_usd_per_token, maximum_expected_cost_pico_usd: stage.maximum_expected_cost_pico_usd }; const kind = card._sources[0]?.kind || 'model'; card.innerHTML = `<div class="stage-card-heading"><strong>TRY ${index + 1}</strong><button type="button" class="quiet-button remove-stage">Remove</button></div><label>Try name<input class="stage-name" maxlength="120"></label><div class="source-mode-row"><label>Find in<select class="source-kind"><option value="model">Models</option><option value="group">Groups</option></select></label><small>Search by name, provider, or model ID.</small></div><div class="source-picker"><button type="button" class="source-search-toggle">+ Find a model or group</button><div class="source-search-popover" hidden><div class="source-search-heading"><strong>Find a model or group</strong><button type="button" class="source-search-close">Close</button></div><input class="source-search" type="search" placeholder="Type to search…" autocomplete="off"><div class="source-candidates" hidden></div></div></div><div class="selected-route-heading"><strong>Selected provider routes</strong><small>All routes are selected by default. Remove or reorder individual provider routes.</small></div><div class="selected-sources"></div><div class="try-settings try-settings-bottom"><label>Retry whole try block<input class="try-retries" type="number" min="0" max="5" step="1"></label><small>Repeat this complete block after its route retries are exhausted.</small></div><small class="stage-explanation">Drag routes to reorder. Duplicate a route for a separate auction discount.</small>`; card.querySelector('.stage-name').value = stage.name || ''; card.querySelector('.source-kind').value = kind; card.querySelector('.try-retries').value = stage.try_retries ?? 1; card.querySelector('.source-search-toggle').addEventListener('click', () => openSourcePickerV4(card)); card.querySelector('.source-search-close').addEventListener('click', () => closeSourcePickerV4(card)); card.querySelector('.source-kind').addEventListener('change', () => { card._sources = []; closeSourcePickerV4(card); renderSelectedSourcesV4(card); renderCandidatesV4(card); previewCurrentGroup(); }); card.querySelector('.source-search').addEventListener('input', () => renderCandidatesV4(card)); card.addEventListener('click', (event) => { const addModelButton = event.target.closest('[data-add-model]'); const candidate = event.target.closest('.source-candidate'); const modelID = addModelButton?.dataset.addModel || (candidate && !event.target.closest('.candidate-add') ? candidate.dataset.modelId : ''); const addGroupButton = event.target.closest('[data-add-group]'); const groupID = addGroupButton?.dataset.addGroup || (candidate && !event.target.closest('.candidate-add') ? candidate.dataset.groupId : ''); if (modelID) { const providers = providerNamesForModel(modelID); providers.forEach((providerNameValue) => card._sources.push({ kind: 'model', model_id: modelID, provider_name: providerNameValue, retries: 1 })); if (!providers.length) card._sources.push({ kind: 'model', model_id: modelID, retries: 1 }); closeSourcePickerV4(card); renderSelectedSourcesV4(card); renderCandidatesV4(card); previewCurrentGroup(); return; } if (groupID) { card._sources = [{ kind: 'group', group_id: groupID, retries: 1 }]; closeSourcePickerV4(card); renderSelectedSourcesV4(card); renderCandidatesV4(card); previewCurrentGroup(); return; } const duplicate = event.target.closest('[data-duplicate-source]'); if (duplicate) { const source = card._sources[Number(duplicate.dataset.duplicateSource)]; if (source) card._sources.splice(Number(duplicate.dataset.duplicateSource) + 1, 0, { ...source }); renderSelectedSourcesV4(card); previewCurrentGroup(); return; } const remove = event.target.closest('[data-remove-source]'); if (remove) { card._sources.splice(Number(remove.dataset.removeSource), 1); renderSelectedSourcesV4(card); renderCandidatesV4(card); previewCurrentGroup(); } }); card.addEventListener('input', (event) => { const row = event.target.closest('.selected-source'); if (!row) return; const source = card._sources[Number(row.dataset.sourceIndex)]; if (!source) return; if (event.target.classList.contains('source-retries')) source.retries = Number(event.target.value || 0); if (event.target.classList.contains('source-auction-percent')) { source.maximum_official_price_percent = Math.max(0, Math.min(100, 100 - Number(event.target.value || 0))); renderAuctionPricingV4(row, source, routeForSource(source)); } }); card.addEventListener('dragstart', (event) => { const row = event.target.closest('.selected-source'); if (row) { event.dataTransfer.setData('text/plain', row.dataset.sourceIndex); row.classList.add('dragging'); } }); card.addEventListener('dragend', (event) => event.target.closest('.selected-source')?.classList.remove('dragging')); card.addEventListener('dragover', (event) => { if (event.target.closest('.selected-source')) event.preventDefault(); }); card.addEventListener('drop', (event) => { const target = event.target.closest('.selected-source'); if (!target) return; event.preventDefault(); const from = Number(event.dataTransfer.getData('text/plain')); const to = Number(target.dataset.sourceIndex); if (from === to || Number.isNaN(from) || Number.isNaN(to)) return; const moved = card._sources.splice(from, 1)[0]; card._sources.splice(to, 0, moved); renderSelectedSourcesV4(card); previewCurrentGroup(); }); renderSelectedSourcesV4(card); renderCandidatesV4(card); return card; }
  function renderGroupStagesV4(definition) { const container = $('#group-stage-list'); if (!container) return; container.replaceChildren(); const stages = definition.stages?.length ? definition.stages : blankGroup().stages; stages.forEach((stage, index) => container.append(stageSourceOptionsV4(stage, index))); }
  function collectGroupDefinitionV4() { const stages = $$('#group-stage-list .group-stage-card').map((card, index) => { const policy = card._stagePolicy || {}; const stage = { position: index, name: card.querySelector('.stage-name').value, sources: (card._sources || []).map((source) => ({ ...source })), billing_classes: policy.billing_classes || ['free', 'subscription', 'metered'], selection: 'lowest_expected_cost', try_retries: Number(card.querySelector('.try-retries').value || 0) }; if (policy.maximum_input_pico_usd_per_token != null) stage.maximum_input_pico_usd_per_token = policy.maximum_input_pico_usd_per_token; if (policy.maximum_output_pico_usd_per_token != null) stage.maximum_output_pico_usd_per_token = policy.maximum_output_pico_usd_per_token; if (policy.maximum_expected_cost_pico_usd != null) stage.maximum_expected_cost_pico_usd = policy.maximum_expected_cost_pico_usd; return stage; }); return { id: $('#group-id').value || undefined, revision: Number($('#group-revision').value || 0), name: $('#group-name').value, slug: $('#group-slug').value, description: $('#group-description').value, enabled: $('#group-enabled').checked, stages }; }
  routePriceText = routePriceTextV4;
  renderGroupStages = renderGroupStagesV4;
  collectGroupDefinition = collectGroupDefinitionV4;

  // V5 picker polish: provider routes are first-class search results, the
  // whole model card remains the bulk action, and route controls are isolated
  // from drag/reorder interactions.
  function routePriceTextV5(route) { if (!route) return 'No provider route discovered'; const access = routeAccessLabelV4(route); const discountValue = route.provider === 'surplus' && route.official_pricing && route.discount_percent_bps != null ? Math.max(0, Math.min(100, Math.round(Number(route.discount_percent_bps || 0) / 100))) : null; const discount = discountValue ? ` · ${discountValue}% discount` : ''; return `${providerName(route.provider)}${access ? ` · ${access}` : ''}${discount} · ${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`; }
  function appendRoutePriceV5(container, route) { const line = document.createElement('span'); line.className = 'route-price-line'; if (!route) { line.textContent = 'No provider route discovered'; container.append(line); return; } const access = routeAccessLabelV4(route); const discountValue = route.provider === 'surplus' && route.official_pricing && route.discount_percent_bps != null ? Math.max(0, Math.min(100, Math.round(Number(route.discount_percent_bps || 0) / 100))) : null; const provider = document.createElement('span'); provider.className = 'route-price-provider'; provider.textContent = providerName(route.provider); line.append(provider); if (access) { const accessNode = document.createElement('span'); accessNode.className = `route-access-label ${access.toLowerCase()}`; accessNode.textContent = access; line.append(accessNode); } if (discountValue) { const discountNode = document.createElement('strong'); discountNode.className = 'route-discount'; discountNode.textContent = `${discountValue}%`; line.append(discountNode); } const price = document.createElement('span'); price.className = 'route-price-values'; price.textContent = `${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`; line.append(price); container.append(line); }
  function renderAuctionPricingV5(row, source, route) { const pricing = row.querySelector('.auction-pricing'); if (!pricing || !route?.official_pricing) return; const discount = Math.max(0, Math.min(100, 100 - Number(source.maximum_official_price_percent ?? 100))); const cap = auctionCapV4(route, discount); const value = pricing.querySelector('.discount-value'); if (value) value.textContent = `${discount}%`; const input = pricing.querySelector('.auction-cap-input'); const output = pricing.querySelector('.auction-cap-output'); if (input) input.textContent = displayPrice(cap.input); if (output) output.textContent = displayPrice(cap.output); }
  function renderCandidatesV5(card) { const list = card.querySelector('.source-candidates'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; const query = (card.querySelector('.source-search')?.value || '').trim().toLowerCase(); list.hidden = !query; if (!query) return; if (kind === 'group') { state.groups.filter((group) => group.id !== $('#group-id').value && `${group.name} ${group.slug}`.toLowerCase().includes(query)).forEach((group) => { const item = document.createElement('div'); item.className = 'source-candidate'; item.dataset.groupId = group.id; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = group.name; const small = document.createElement('small'); small.textContent = group.slug; main.append(strong, small); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addGroup = group.id; add.textContent = 'Add group'; item.append(main, add); list.append(item); }); } else { modelCandidates().filter((model) => `${model.name} ${model.id} ${model.routes.map((route) => route.provider).join(' ')}`.toLowerCase().includes(query)).forEach((model) => { const item = document.createElement('div'); item.className = 'source-candidate'; item.dataset.modelId = model.id; const selected = (card._sources || []).some((source) => source.kind === 'model' && source.model_id === model.id); if (selected) item.classList.add('selected'); const main = document.createElement('div'); main.className = 'source-candidate-main'; const titleRow = document.createElement('div'); titleRow.className = 'source-candidate-title-row'; const strong = document.createElement('strong'); strong.textContent = model.name; const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addModel = model.id; add.textContent = selected ? 'Add all routes again' : 'Add all routes'; titleRow.append(strong, add); const small = document.createElement('small'); small.textContent = model.id; main.append(titleRow, small); const routes = document.createElement('div'); routes.className = 'source-route-list'; model.routes.forEach((route) => { const option = document.createElement('button'); option.type = 'button'; option.className = `source-route-option ${routeBilling(route)}`; option.dataset.addModel = model.id; option.dataset.addProvider = route.provider; option.title = `Add ${providerName(route.provider)} route`; appendRoutePriceV5(option, route); const action = document.createElement('span'); action.className = 'source-route-option-action'; action.textContent = 'Add'; option.append(action); routes.append(option); }); main.append(routes); item.append(main); list.append(item); }); } if (!list.children.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = 'No matching models or groups.'; list.append(empty); } }
  function renderSelectedSourcesV5(card) { const list = card.querySelector('.selected-sources'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; card._sources = (card._sources || []).filter((source) => source.kind === kind); if (!card._sources.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = kind === 'model' ? 'Selected provider routes will appear here.' : 'Selected group will appear here.'; list.append(empty); return; } card._sources.forEach((source, sourceIndex) => { const row = document.createElement('div'); row.className = 'selected-source'; row.draggable = false; row.dataset.sourceIndex = sourceIndex; const handle = document.createElement('span'); handle.className = 'drag-handle'; handle.textContent = '☷'; handle.draggable = true; handle.title = 'Drag to reorder'; const main = document.createElement('div'); main.className = 'selected-source-main'; const model = kind === 'model' ? modelDisplay(source.model_id) : null; const title = document.createElement('strong'); title.textContent = kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group'); const provider = document.createElement('span'); provider.className = 'selected-route-provider'; provider.textContent = kind === 'model' ? providerName(source.provider_name || 'provider') : 'Group fallback'; const meta = document.createElement('small'); meta.textContent = kind === 'model' ? source.model_id : (state.groups.find((group) => group.id === source.group_id)?.slug || ''); main.append(title, provider, meta); const route = kind === 'model' ? routeForSource(source) : null; if (kind === 'model') appendRoutePriceV5(main, route); const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'source-remove'; remove.dataset.removeSource = sourceIndex; remove.textContent = 'Remove'; const controls = document.createElement('div'); controls.className = 'selected-source-controls'; if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) { const pricing = document.createElement('div'); pricing.className = 'route-setting-section auction-pricing'; const heading = document.createElement('div'); heading.className = 'route-setting-heading'; heading.innerHTML = '<strong>Min discount</strong><output class="discount-value"></output>'; pricing.append(heading); const slider = document.createElement('input'); slider.type = 'range'; slider.min = '0'; slider.max = '100'; slider.step = '1'; slider.className = 'source-auction-percent'; slider.value = 100 - Number(source.maximum_official_price_percent ?? 100); pricing.append(slider); const cap = document.createElement('small'); cap.className = 'auction-cap'; cap.innerHTML = 'Price cap <strong class="auction-cap-input"></strong> in / <strong class="auction-cap-output"></strong> out'; pricing.append(cap); controls.append(pricing); } const reliability = document.createElement('div'); reliability.className = 'route-setting-section route-reliability'; const retryHeading = document.createElement('strong'); retryHeading.className = 'route-setting-heading'; retryHeading.textContent = 'Reliability'; const retryLabel = document.createElement('label'); retryLabel.textContent = 'Retries'; const retry = document.createElement('input'); retry.type = 'number'; retry.min = '0'; retry.max = '5'; retry.step = '1'; retry.className = 'source-retries'; retry.value = source.retries ?? 1; retryLabel.append(retry); const duplicate = document.createElement('button'); duplicate.type = 'button'; duplicate.className = 'source-action'; duplicate.dataset.duplicateSource = sourceIndex; duplicate.textContent = 'Duplicate'; reliability.append(retryHeading, retryLabel, duplicate); controls.append(reliability); row.append(handle, main, remove, controls); list.append(row); if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) renderAuctionPricingV5(row, source, route); }); }
  renderCandidatesV4 = renderCandidatesV5;
  renderSelectedSourcesV4 = renderSelectedSourcesV5;
  appendRoutePriceV4 = appendRoutePriceV5;
  routePriceTextV4 = routePriceTextV5;
  renderAuctionPricingV4 = renderAuctionPricingV5;
  routePriceText = routePriceTextV5;
  function renderGroupsV6() { const list = $('#groups-list'); if (!list) return; const query = ($('#groups-search')?.value || '').toLowerCase().trim(); list.replaceChildren(); const groups = state.groups.filter((group) => !query || `${group.name} ${group.slug} ${JSON.stringify(group.stages)}`.toLowerCase().includes(query)); setText('#groups-count', formatNumber(state.groups.filter((group) => group.enabled).length)); setText('#groups-warning-count', formatNumber(state.groups.filter((group) => !group.enabled || !group.stages?.length).length)); groups.forEach((group) => { const row = document.createElement('div'); row.className = `group-row ${group.enabled ? 'is-enabled' : 'is-disabled'}`; const main = document.createElement('div'); main.className = 'group-main'; const title = document.createElement('strong'); title.textContent = group.name; const slug = document.createElement('code'); slug.textContent = group.slug; const note = document.createElement('small'); const count = group.stages?.length || 0; note.textContent = `${count} route stage${count === 1 ? '' : 's'}`; main.append(title, slug, note); row.append(main); const summary = document.createElement('span'); summary.className = 'group-summary'; summary.textContent = count ? `${count} route stage${count === 1 ? '' : 's'}` : 'Needs a route'; row.append(summary); const actions = document.createElement('div'); actions.className = 'group-actions'; const toggle = document.createElement('button'); toggle.type = 'button'; toggle.className = `group-toggle ${group.enabled ? 'enabled' : 'disabled'}`; toggle.dataset.toggleGroup = group.id; toggle.setAttribute('role', 'switch'); toggle.setAttribute('aria-checked', group.enabled ? 'true' : 'false'); toggle.textContent = group.enabled ? 'Enabled' : 'Disabled'; toggle.title = group.enabled ? 'Disable group' : 'Enable group'; const edit = document.createElement('button'); edit.type = 'button'; edit.className = 'quiet-button'; edit.dataset.editGroup = group.id; edit.textContent = 'Edit'; const copy = document.createElement('button'); copy.type = 'button'; copy.className = 'quiet-button'; copy.dataset.copyGroupSlug = group.slug; copy.textContent = 'Copy slug'; actions.append(toggle, edit, copy); row.append(actions); list.append(row); }); if (!groups.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = state.groups.length ? 'No groups match your search.' : 'Create a group alias for a routing strategy.'; list.append(empty); } }
  renderGroups = renderGroupsV6;
  async function toggleGroupV6(button) { const id = button?.dataset.toggleGroup; const group = state.groups.find((item) => item.id === id); if (!group) return; const enabled = !group.enabled; button.disabled = true; button.textContent = 'Saving…'; try { const payload = await fetchJSON(`/api/groups/${encodeURIComponent(id)}?revision=${encodeURIComponent(group.revision || 0)}`, { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ ...group, enabled }) }); const saved = payload.data || { ...group, enabled, revision: Number(group.revision || 0) + 1 }; state.groups = state.groups.map((item) => item.id === id ? saved : item); renderGroupsV6(); } catch (error) { button.disabled = false; button.textContent = group.enabled ? 'Enabled' : 'Disabled'; button.title = error.message; } }
  document.addEventListener('click', (event) => { const toggle = event.target.closest('[data-toggle-group]'); if (!toggle) return; event.preventDefault(); event.stopImmediatePropagation(); toggleGroupV6(toggle); });

  function renderGroupsV7() {
    const list = $('#groups-list');
    if (!list) return;
    const query = ($('#groups-search')?.value || '').toLowerCase().trim();
    list.replaceChildren();
    const groups = state.groups.filter((group) => !query || `${group.name} ${group.slug} ${JSON.stringify(group.stages)}`.toLowerCase().includes(query));
    setText('#groups-count', formatNumber(state.groups.filter((group) => group.enabled).length));
    setText('#groups-warning-count', formatNumber(state.groups.filter((group) => !group.enabled || !group.stages?.length).length));
    groups.forEach((group) => {
      const row = document.createElement('div');
      row.className = `group-row ${group.enabled ? 'is-enabled' : 'is-disabled'}`;
      const main = document.createElement('div');
      main.className = 'group-main';
      const title = document.createElement('strong');
      title.textContent = group.name;
      const titleRow = document.createElement('div');
      titleRow.className = 'group-title-row';
      const slugRow = document.createElement('div');
      slugRow.className = 'group-slug-row';
      const slug = document.createElement('code');
      slug.textContent = group.slug;
      const copy = document.createElement('button');
      copy.type = 'button';
      copy.className = 'group-copy';
      copy.dataset.copyGroupSlug = group.slug;
      copy.dataset.copyLabel = '⧉';
      copy.dataset.copiedLabel = '✓';
      copy.setAttribute('aria-label', 'Copy group slug');
      copy.textContent = '⧉';
      copy.title = 'Copy group slug';
      slugRow.append(slug, copy);
      const connect = document.createElement('button');
      connect.type = 'button';
      connect.className = 'group-connect-link';
      connect.dataset.connectGroup = group.id;
      connect.setAttribute('aria-label', `Connection instructions for ${group.name}`);
      connect.title = 'Connection instructions';
      const connectIcon = document.createElement('span');
      connectIcon.className = 'group-connect-help';
      connectIcon.setAttribute('aria-hidden', 'true');
      connectIcon.textContent = '?';
      const connectLabel = document.createElement('span');
      connectLabel.className = 'group-connect-label';
      connectLabel.textContent = 'Connection instructions';
      const connectArrow = document.createElement('span');
      connectArrow.className = 'group-connect-arrow';
      connectArrow.setAttribute('aria-hidden', 'true');
      connectArrow.textContent = '→';
      connect.append(connectIcon, connectLabel, connectArrow);
      const note = document.createElement('small');
      const count = group.stages?.length || 0;
      note.textContent = `${count} route stage${count === 1 ? '' : 's'}`;
      const toggle = document.createElement('button');
      toggle.type = 'button';
      toggle.className = `group-toggle ${group.enabled ? 'enabled' : 'disabled'}`;
      toggle.dataset.toggleGroup = group.id;
      toggle.setAttribute('role', 'switch');
      toggle.setAttribute('aria-checked', group.enabled ? 'true' : 'false');
      toggle.setAttribute('aria-label', `${group.enabled ? 'Disable' : 'Enable'} ${group.name}`);
      toggle.title = group.enabled ? 'Disable group' : 'Enable group';
      const track = document.createElement('span');
      track.className = 'group-toggle-track';
      track.setAttribute('aria-hidden', 'true');
      toggle.append(track);
      titleRow.append(title, toggle);
      main.append(titleRow, slugRow, connect, note);
      row.append(main);

      const actions = document.createElement('div');
      actions.className = 'group-actions';
      const edit = document.createElement('button');
      edit.type = 'button';
      edit.className = 'quiet-button';
      edit.dataset.editGroup = group.id;
      edit.textContent = 'Edit';
      actions.append(edit);
      row.append(actions);
      list.append(row);
    });
    if (!groups.length) {
      const empty = document.createElement('div');
      empty.className = 'empty-state';
      empty.textContent = state.groups.length ? 'No groups match your search.' : 'Create a group alias for a routing strategy.';
      list.append(empty);
    }
  }
  renderGroupsV6 = renderGroupsV7;
  renderGroups = renderGroupsV7;
  function slugifyGroupName(value) {
    return String(value || '').normalize('NFKD').replace(/[\u0300-\u036f]/g, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 128);
  }
  function suggestedGroupName() {
    const entries = [];
    const seen = new Set();
    $$('#group-stage-list .group-stage-card').forEach((card) => (card._sources || []).forEach((source) => {
      const key = source.kind === 'group' ? `group:${source.group_id}` : `model:${source.model_id}`;
      if (seen.has(key)) return;
      seen.add(key);
      if (source.kind === 'group') entries.push(state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Group');
      else entries.push(modelDisplay(source.model_id).name || source.model_id || 'Model');
    }));
    if (!entries.length) return '';
    if (entries.length === 1) return entries[0];
    const extra = entries.length - 1;
    return `${entries[0]} + ${extra} other ${extra === 1 ? 'model' : 'models'}`;
  }
  function refreshSuggestedGroupIdentity() {
    const name = $('#group-name');
    const slug = $('#group-slug');
    if (!name || !slug || name.dataset.userEdited === 'true') return;
    const suggestion = suggestedGroupName();
    if (!suggestion) return;
    name.dataset.autofill = 'true';
    name.value = suggestion;
    delete name.dataset.autofill;
    if (slug.dataset.userEdited !== 'true') {
      slug.dataset.autofill = 'true';
      slug.value = slugifyGroupName(suggestion);
      delete slug.dataset.autofill;
    }
  }
  function arrangeGroupIdentityFields() {
    const form = $('#group-form');
    if (!form || $('#group-identity-fields')) return;
    const nameLabel = form.querySelector('label[for="group-name"]');
    const name = $('#group-name');
    const slugLabel = form.querySelector('label[for="group-slug"]');
    const slug = $('#group-slug');
    const note = slug?.nextElementSibling;
    const preview = $('#group-preview');
    if (!nameLabel || !name || !slugLabel || !slug || !preview) return;
    const fields = document.createElement('div');
    fields.id = 'group-identity-fields';
    fields.className = 'group-identity-fields';
    fields.append(nameLabel, name, slugLabel, slug);
    if (note?.tagName === 'SMALL') fields.append(note);
    form.insertBefore(fields, preview);
  }
  arrangeGroupIdentityFields();
  function removeRedundantGroupUI() {
    $('#group-preview')?.remove();
    const groupsPanel = document.querySelector('[data-view-panel="groups"]');
    const callingCard = [...(groupsPanel?.querySelectorAll('.summary-item') || [])]
      .find((item) => item.querySelector('span')?.textContent.trim() === 'CALLING A GROUP');
    callingCard?.remove();
  }
  removeRedundantGroupUI();
  const groupNameInput = $('#group-name');
  const groupSlugInput = $('#group-slug');
  groupNameInput?.addEventListener('input', () => {
    if (groupNameInput.dataset.autofill !== 'true') groupNameInput.dataset.userEdited = 'true';
    if (groupNameInput.dataset.autofill !== 'true' && groupSlugInput?.dataset.userEdited !== 'true') groupSlugInput.value = slugifyGroupName(groupNameInput.value);
  });
  groupSlugInput?.addEventListener('input', () => { if (groupSlugInput.dataset.autofill !== 'true') groupSlugInput.dataset.userEdited = 'true'; });
  document.addEventListener('click', (event) => {
    if (event.target.closest('#group-stage-list [data-add-model], #group-stage-list [data-add-group], #group-stage-list .source-candidate[data-model-id], #group-stage-list .source-candidate[data-group-id]')) setTimeout(refreshSuggestedGroupIdentity, 0);
  }, true);
  const openGroupEditorOriginal = openGroupEditor;
  openGroupEditor = function(definition = blankGroup()) {
    const editor = $('#group-editor');
    const creating = !definition.id;
    openGroupEditorOriginal(definition);
    if (editor) {
      if (creating) {
        editor.dataset.closeAfterCreate = 'true';
        if (groupNameInput) { groupNameInput.dataset.userEdited = 'false'; groupNameInput.value = ''; }
        if (groupSlugInput) { groupSlugInput.dataset.userEdited = 'false'; groupSlugInput.value = ''; }
      } else {
        if (groupNameInput) groupNameInput.dataset.userEdited = 'true';
        if (groupSlugInput) groupSlugInput.dataset.userEdited = 'true';
        if (editor.dataset.closeAfterCreate === 'true') {
          editor.hidden = true;
          delete editor.dataset.closeAfterCreate;
        }
      }
    }
  };

  function appendRouteSummaryV6(container, route) { const line = document.createElement('span'); line.className = 'route-price-line selected-route-summary'; if (!route) { line.textContent = 'No provider route discovered'; container.append(line); return; } const access = routeAccessLabelV4(route); const discountValue = route.provider === 'surplus' && route.official_pricing && route.discount_percent_bps != null ? Math.max(0, Math.min(100, Math.round(Number(route.discount_percent_bps || 0) / 100))) : null; if (access) { const accessNode = document.createElement('span'); accessNode.className = `route-access-label ${access.toLowerCase()}`; accessNode.textContent = access; line.append(accessNode); } if (discountValue) { const discountNode = document.createElement('strong'); discountNode.className = 'route-discount'; discountNode.textContent = `${discountValue}%`; line.append(discountNode); } const price = document.createElement('span'); price.className = 'route-price-values'; price.textContent = `${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`; line.append(price); container.append(line); }
  function ensureRetryModalV6() { return $('#retry-modal'); }
  function openRetryModalV6(card, sourceIndex) { const modal = ensureRetryModalV6(); const source = card?._sources?.[sourceIndex]; if (!modal || !source) return; state.retryTarget = { card, sourceIndex }; const input = $('#retry-count'); if (input) input.value = Math.max(1, Math.min(5, Number(source.retries ?? 1))); openModal('retry-modal'); }
  function renderSelectedSourcesV6(card) { const list = card.querySelector('.selected-sources'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; card._sources = (card._sources || []).filter((source) => source.kind === kind); if (!card._sources.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = kind === 'model' ? 'Selected provider routes will appear here.' : 'Selected group will appear here.'; list.append(empty); return; } card._sources.forEach((source, sourceIndex) => { const row = document.createElement('div'); row.className = 'selected-source'; row.draggable = false; row.dataset.sourceIndex = sourceIndex; const handle = document.createElement('span'); handle.className = 'drag-handle'; handle.textContent = '☷'; handle.draggable = true; handle.title = 'Drag to reorder'; const main = document.createElement('div'); main.className = 'selected-source-main'; const header = document.createElement('div'); header.className = 'selected-source-header'; const title = document.createElement('strong'); const model = kind === 'model' ? modelDisplay(source.model_id) : null; title.textContent = kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group'); const actions = document.createElement('div'); actions.className = 'selected-source-header-actions'; const retryButton = document.createElement('button'); retryButton.type = 'button'; retryButton.className = 'retry-summary'; retryButton.dataset.retrySource = sourceIndex; retryButton.setAttribute('aria-label', `Configure retries, currently ${Math.max(1, Number(source.retries ?? 1))}`); retryButton.innerHTML = `<span aria-hidden="true">↻</span><span>${Math.max(1, Number(source.retries ?? 1))}</span>`; actions.append(retryButton); const duplicate = document.createElement('button'); duplicate.type = 'button'; duplicate.className = 'source-action source-duplicate'; duplicate.dataset.duplicateSource = sourceIndex; duplicate.textContent = 'Duplicate'; actions.append(duplicate); header.append(title, actions); const provider = document.createElement('strong'); provider.className = 'selected-route-provider'; provider.textContent = kind === 'model' ? providerName(source.provider_name || 'provider') : 'Group fallback'; const meta = document.createElement('small'); meta.textContent = kind === 'model' ? source.model_id : (state.groups.find((group) => group.id === source.group_id)?.slug || ''); main.append(header, provider, meta); const route = kind === 'model' ? routeForSource(source) : null; if (kind === 'model') appendRouteSummaryV6(main, route); const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'source-remove'; remove.dataset.removeSource = sourceIndex; remove.textContent = 'Remove'; const controls = document.createElement('div'); controls.className = 'selected-source-controls'; if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) { const pricing = document.createElement('div'); pricing.className = 'route-setting-section auction-pricing'; const heading = document.createElement('div'); heading.className = 'route-setting-heading'; heading.innerHTML = '<strong>Min discount</strong><output class="discount-value"></output>'; pricing.append(heading); const slider = document.createElement('input'); slider.type = 'range'; slider.min = '0'; slider.max = '100'; slider.step = '1'; slider.className = 'source-auction-percent'; slider.value = 100 - Number(source.maximum_official_price_percent ?? 100); pricing.append(slider); const cap = document.createElement('small'); cap.className = 'auction-cap'; cap.innerHTML = 'Price cap <strong class="auction-cap-input"></strong> in / <strong class="auction-cap-output"></strong> out'; pricing.append(cap); controls.append(pricing); } row.append(handle, main, remove, controls); list.append(row); if (kind === 'model' && route?.provider === 'surplus' && route.official_pricing) renderAuctionPricingV5(row, source, route); }); }
  renderSelectedSourcesV4 = renderSelectedSourcesV6;

  // Route pricing is shown consistently for every provider. Auction routes
  // are not the only routes with a reference price: subscription and metered
  // providers can also be cheaper than the official catalog baseline.
  function routeDiscountInfoV7(route) {
    if (!route?.official_pricing) return null;
    const officialInput = Number(route.official_pricing.input);
    const officialOutput = Number(route.official_pricing.output);
    const currentInput = Number(route.pricing?.input);
    const currentOutput = Number(route.pricing?.output);
    const samePrice = Number.isFinite(officialInput) && Number.isFinite(officialOutput) && currentInput === officialInput && currentOutput === officialOutput;
    let percent = route.discount_percent_bps == null ? null : Math.round(Number(route.discount_percent_bps) / 100);
    if (percent == null || !Number.isFinite(percent)) {
      const ratios = [];
      if (officialInput > 0 && Number.isFinite(currentInput)) ratios.push(currentInput / officialInput);
      if (officialOutput > 0 && Number.isFinite(currentOutput)) ratios.push(currentOutput / officialOutput);
      if (ratios.length) percent = Math.round((1 - ratios.reduce((sum, ratio) => sum + ratio, 0) / ratios.length) * 100);
    }
    if (percent == null || !Number.isFinite(percent)) return null;
    percent = Math.max(0, Math.min(100, percent));
    return { percent, label: percent === 0 && samePrice ? 'Official price' : percent > 0 ? `-${percent}%` : '0%' };
  }

  function appendRoutePriceV7(container, route) {
    const line = document.createElement('span');
    line.className = 'route-price-line';
    if (!route) {
      line.textContent = 'No provider route discovered';
      container.append(line);
      return;
    }
    const access = routeAccessLabelV4(route);
    const provider = document.createElement('span');
    provider.className = 'route-price-provider';
    provider.textContent = providerName(route.provider);
    line.append(provider);
    if (access) {
      const accessNode = document.createElement('span');
      accessNode.className = `route-access-label ${access.toLowerCase()}`;
      accessNode.textContent = access;
      line.append(accessNode);
    }
    // Subscription routes expose their access class only. Their displayed
    // prices and discounts are not meaningful for a subscription allowance.
    if (routeBilling(route) === 'subscription') {
      container.append(line);
      return;
    }
    const price = document.createElement('span');
    price.className = 'route-price-values';
    price.textContent = `${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`;
    line.append(price);
    const discount = routeDiscountInfoV7(route);
    if (discount) {
      const discountNode = document.createElement('strong');
      discountNode.className = `route-discount${discount.label === 'Official price' ? ' official' : ''}`;
      discountNode.textContent = discount.label;
      line.append(discountNode);
    }
    container.append(line);
  }

  appendRoutePriceV5 = appendRoutePriceV7;
  appendRoutePriceV4 = appendRoutePriceV7;
  routePriceTextV5 = (route) => {
    if (!route) return 'No provider route discovered';
    const access = routeAccessLabelV4(route);
    const discount = routeDiscountInfoV7(route);
    return `${providerName(route.provider)}${access ? ` · ${access}` : ''} · ${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out${discount ? ` · ${discount.label}` : ''}`;
  };
  routePriceText = routePriceTextV5;

  appendRouteSummaryV6 = function(container, route) {
    const line = document.createElement('span');
    line.className = 'route-price-line selected-route-summary';
    if (!route) {
      line.textContent = 'No provider route discovered';
      container.append(line);
      return;
    }
    const access = routeAccessLabelV4(route);
    if (access) {
      const accessNode = document.createElement('span');
      accessNode.className = `route-access-label ${access.toLowerCase()}`;
      accessNode.textContent = access;
      line.append(accessNode);
    }
    const price = document.createElement('span');
    price.className = 'route-price-values';
    price.textContent = `${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`;
    line.append(price);
    const discount = routeDiscountInfoV7(route);
    if (discount) {
      const discountNode = document.createElement('strong');
      discountNode.className = `route-discount${discount.label === 'Official price' ? ' official' : ''}`;
      discountNode.textContent = discount.label;
      line.append(discountNode);
    }
    container.append(line);
  };

  // Keep route controls compact and discoverable without text-heavy buttons.
  const renderSelectedSourcesV6Original = renderSelectedSourcesV6;
  function decorateRouteActionsV7(card) {
    card.querySelector('.selected-sources .source-empty')?.remove();
    card.querySelectorAll('.source-duplicate').forEach((button) => {
      button.classList.add('route-icon-button');
      button.setAttribute('aria-label', 'Duplicate provider route');
      button.title = 'Duplicate provider route';
      button.innerHTML = '<span aria-hidden="true">⧉</span>';
    });
    card.querySelectorAll('.source-remove').forEach((button) => {
      button.classList.add('route-icon-button');
      button.setAttribute('aria-label', 'Remove provider route');
      button.title = 'Remove provider route';
      button.innerHTML = '<span aria-hidden="true">×</span>';
      const actions = button.closest('.selected-source')?.querySelector('.selected-source-header-actions');
      if (actions && button.parentElement !== actions) actions.append(button);
    });
  }
  renderSelectedSourcesV6 = function(card) {
    renderSelectedSourcesV6Original(card);
    decorateRouteActionsV7(card);
  };
  renderSelectedSourcesV4 = renderSelectedSourcesV6;

  const stageSourceOptionsV4Original = stageSourceOptionsV4;
  stageSourceOptionsV4 = function(stage, index) { const card = stageSourceOptionsV4Original(stage, index); const stageName = card.querySelector('.stage-name'); if (stageName) { stageName.type = 'hidden'; stageName.closest('label')?.replaceWith(stageName); } card.addEventListener('click', (event) => { const route = event.target.closest('[data-add-provider]'); if (route) { event.stopImmediatePropagation(); const modelID = route.dataset.addModel; const provider = route.dataset.addProvider; if (!modelID || !provider) return; card._sources.push({ kind: 'model', model_id: modelID, provider_name: provider, retries: 1 }); closeSourcePickerV4(card); renderSelectedSourcesV4(card); renderCandidatesV4(card); previewCurrentGroup(); return; } const retry = event.target.closest('[data-retry-source]'); if (retry) { event.preventDefault(); openRetryModalV6(card, Number(retry.dataset.retrySource)); } }, true); card.addEventListener('dragstart', (event) => { if (!event.target.closest('.drag-handle')) event.stopImmediatePropagation(); }, true); return card; };

  // The route block searches one combined catalog. Keep the source-kind
  // select as an internal compatibility field, but do not make users choose
  // a mode before they can search.
  function renderCandidatesV8(card) {
    const list = card.querySelector('.source-candidates');
    if (!list) return;
    list.replaceChildren();
    const query = (card.querySelector('.source-search')?.value || '').trim().toLowerCase();
    list.hidden = !query;
    if (!query) return;
    modelCandidates().filter((model) => `${model.name} ${model.id} ${model.routes.map((route) => route.provider).join(' ')}`.toLowerCase().includes(query)).forEach((model) => {
      const item = document.createElement('div');
      item.className = 'source-candidate';
      item.dataset.modelId = model.id;
      if ((card._sources || []).some((source) => source.kind === 'model' && source.model_id === model.id)) item.classList.add('selected');
      const main = document.createElement('div');
      main.className = 'source-candidate-main';
      const titleRow = document.createElement('div');
      titleRow.className = 'source-candidate-title-row';
      const strong = document.createElement('strong');
      strong.textContent = model.name;
      const add = document.createElement('button');
      add.type = 'button';
      add.className = 'candidate-add';
      add.dataset.addModel = model.id;
      add.textContent = item.classList.contains('selected') ? 'Add all routes again' : 'Add all routes';
      titleRow.append(strong, add);
      const small = document.createElement('small');
      small.textContent = model.id;
      main.append(titleRow, small);
      const routes = document.createElement('div');
      routes.className = 'source-route-list';
      model.routes.forEach((route) => {
        const option = document.createElement('button');
        option.type = 'button';
        option.className = `source-route-option ${routeBilling(route)}`;
        option.dataset.addModel = model.id;
        option.dataset.addProvider = route.provider;
        option.title = `Add ${providerName(route.provider)} route`;
        appendRoutePriceV7(option, route);
        const action = document.createElement('span');
        action.className = 'source-route-option-action';
        action.textContent = 'Add';
        option.append(action);
        routes.append(option);
      });
      main.append(routes);
      item.append(main);
      list.append(item);
    });
    state.groups.filter((group) => group.id !== $('#group-id').value && `${group.name} ${group.slug}`.toLowerCase().includes(query)).forEach((group) => {
      const item = document.createElement('div');
      item.className = 'source-candidate source-group-candidate';
      item.dataset.groupId = group.id;
      const main = document.createElement('div');
      main.className = 'source-candidate-main';
      const titleRow = document.createElement('div');
      titleRow.className = 'source-candidate-title-row';
      const strong = document.createElement('strong');
      strong.textContent = group.name;
      const add = document.createElement('button');
      add.type = 'button';
      add.className = 'candidate-add';
      add.dataset.addGroup = group.id;
      add.textContent = 'Add group';
      titleRow.append(strong, add);
      const small = document.createElement('small');
      small.textContent = group.slug;
      main.append(titleRow, small);
      item.append(main);
      list.append(item);
    });
    // Groups are higher-level routing aliases, so surface them before the
    // underlying model results when both match the same search.
    const matchingGroups = [...list.querySelectorAll('.source-group-candidate')];
    matchingGroups.reverse().forEach((group) => list.prepend(group));
    if (!list.children.length) {
      const empty = document.createElement('small');
      empty.className = 'source-empty';
      empty.textContent = 'No matching models or groups.';
      list.append(empty);
    }
  }
  renderCandidatesV4 = renderCandidatesV8;
  renderCandidatesV5 = renderCandidatesV8;

  function setRetryModalCopyV8(kind) {
    const title = $('#retry-modal-title');
    const note = $('#retry-modal')?.querySelector('.modal-note');
    if (kind === 'block') {
      if (title) title.textContent = 'Retry this route block';
      if (note) note.textContent = 'Repeat the complete route block after its provider routes are exhausted.';
    } else {
      if (title) title.textContent = 'Retry this route';
      if (note) note.textContent = 'Retry the same provider route before moving to the next route. Most routes work well with the default.';
    }
  }
  function openTryRetryModalV8(card) {
    const modal = ensureRetryModalV6();
    const input = $('#retry-count');
    const current = card.querySelector('.try-retries');
    if (!modal || !input || !current) return;
    state.retryTarget = null;
    state.tryRetryTarget = card;
    setRetryModalCopyV8('block');
    input.value = Math.max(1, Math.min(5, Number(current.value || 1)));
    openModal('retry-modal');
  }
  const openRetryModalV6OriginalV8 = openRetryModalV6;
  openRetryModalV6 = function(card, sourceIndex) {
    state.tryRetryTarget = null;
    setRetryModalCopyV8('route');
    return openRetryModalV6OriginalV8(card, sourceIndex);
  };
  $('#retry-form')?.addEventListener('submit', (event) => {
    const card = state.tryRetryTarget;
    if (!card) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    const input = $('#retry-count');
    const target = card.querySelector('.try-retries');
    if (input && target) {
      const retries = Math.max(1, Math.min(5, Number(input.value || 1)));
      target.value = retries;
      const summary = card.querySelector('.try-retry-summary');
      if (summary) {
        summary.querySelector('.retry-count-value').textContent = retries;
        summary.setAttribute('aria-label', `Configure route block retries, currently ${retries}`);
      }
    }
    previewCurrentGroup();
    closeModal('retry-modal');
    state.tryRetryTarget = null;
    setRetryModalCopyV8('route');
  }, true);

  const stageSourceOptionsV8Original = stageSourceOptionsV4;
  stageSourceOptionsV4 = function(stage, index) {
    const card = stageSourceOptionsV8Original(stage, index);
    const heading = card.querySelector('.stage-card-heading > strong');
    if (heading) heading.textContent = `ROUTE BLOCK ${index + 1}`;
    const removeStage = card.querySelector('.remove-stage');
    if (removeStage) {
      removeStage.classList.add('route-icon-button');
      removeStage.setAttribute('aria-label', 'Remove route block');
      removeStage.title = 'Remove route block';
      removeStage.textContent = '×';
    }
    const modeRow = card.querySelector('.source-mode-row');
    const modeSelect = modeRow?.querySelector('.source-kind');
    if (modeSelect) {
      modeSelect.hidden = true;
      card.append(modeSelect);
    }
    modeRow?.remove();
    const sourceSearch = card.querySelector('.source-search');
    if (sourceSearch) sourceSearch.placeholder = 'Search models or groups…';
    card.querySelector('.selected-route-heading')?.remove();
    card.querySelector('.stage-explanation')?.remove();
    const retryPanel = card.querySelector('.try-settings-bottom');
    const retryInput = retryPanel?.querySelector('.try-retries');
    if (retryPanel && retryInput) {
      const retries = Math.max(1, Math.min(5, Number(retryInput.value || 1)));
      retryPanel.replaceChildren();
      retryPanel.hidden = true;
      const summary = document.createElement('button');
      summary.type = 'button';
      summary.className = 'retry-summary try-retry-summary';
      summary.setAttribute('aria-label', `Configure route block retries, currently ${retries}`);
      summary.title = 'Configure route block retries';
      summary.innerHTML = '<span aria-hidden="true">↻</span><span class="retry-count-value"></span>';
      summary.querySelector('.retry-count-value').textContent = retries;
      summary.addEventListener('click', () => openTryRetryModalV8(card));
      retryInput.hidden = true;
      retryInput.value = retries;
      retryPanel.append(retryInput);
      const headingRow = card.querySelector('.stage-card-heading');
      const stageActions = document.createElement('div');
      stageActions.className = 'stage-card-actions';
      stageActions.append(summary);
      const duplicateStage = document.createElement('button');
      duplicateStage.type = 'button';
      duplicateStage.className = 'source-duplicate route-icon-button';
      duplicateStage.dataset.duplicateStage = String(index);
      duplicateStage.setAttribute('aria-label', 'Duplicate route block');
      duplicateStage.title = 'Duplicate route block';
      duplicateStage.innerHTML = '<span aria-hidden="true">⧉</span>';
      stageActions.append(duplicateStage);
      if (removeStage) stageActions.append(removeStage);
      headingRow?.append(stageActions);
    }
    card.addEventListener('click', (event) => {
      const model = event.target.closest('[data-add-model], .source-candidate[data-model-id]');
      const group = event.target.closest('[data-add-group], .source-group-candidate');
      const select = card.querySelector('.source-kind');
      if (model) {
        const modelID = model.dataset.addModel || model.dataset.modelId || model.closest('.source-candidate')?.dataset.modelId;
        if (modelID) {
          event.preventDefault();
          event.stopImmediatePropagation();
          // Clicking the model adds each currently discovered provider-model
          // route as an independent, reorderable source. Subscription and
          // free routes remain visible; only the special future-provider row
          // is represented without a concrete provider.
          const routeSources = providerRoutesForModel(modelID)
            .slice()
            .sort((a, b) => {
              const priority = (route) => route.free ? 0 : routeBilling(route) === 'subscription' ? 1 : 2;
              return priority(a) - priority(b);
            })
            .map((route) => ({ kind: 'model', model_id: modelID, provider_name: route.provider, retries: 1 }));
          card._sources.push(...routeSources);
          card._sources.push({ kind: 'model', model_id: modelID, retries: 1, include_new_providers: true });
          closeSourcePickerV4(card);
          renderSelectedSourcesV4(card);
          renderCandidatesV4(card);
          previewCurrentGroup();
          return;
        }
      }
      if (select && (model || group)) select.value = group ? 'group' : 'model';
    }, true);
    card.addEventListener('input', (event) => {
      const slider = event.target.closest('.source-max-price-percent');
      if (!slider) return;
      const row = slider.closest('.selected-source');
      const source = card._sources?.[Number(row?.dataset.sourceIndex)];
      if (!source) return;
      event.stopImmediatePropagation();
      source.maximum_official_price_percent = Math.max(0, Math.min(100, Number(slider.value || 0)));
      // Keep the native range input mounted while the pointer is dragging.
      // Re-rendering the selected-source list here detaches the input after
      // the first `input` event, which makes a drag stop at the first pixel.
      const capRoutes = sourceRoutesForModel(source).filter((route) => route.official_pricing);
      if (capRoutes.length) renderAllProviderPriceCapV9(row, source, capRoutes);
      previewCurrentGroup();
    }, true);
    card.addEventListener('change', (event) => {
      const checkbox = event.target.closest('.source-include-new');
      if (!checkbox) return;
      const row = checkbox.closest('.selected-source');
      const source = card._sources?.[Number(row?.dataset.sourceIndex)];
      if (!source) return;
      event.stopImmediatePropagation();
      source.include_new_providers = checkbox.checked;
      if (!checkbox.checked && !source.provider_names?.length) source.provider_names = providerNamesForModel(source.model_id);
      renderSelectedSourcesV4(card);
      previewCurrentGroup();
    }, true);
    return card;
  };
  function modelOfficialPricing(routes) {
    const canonical = routes.find((route) => route.provider === 'openrouter' && route.official_pricing);
    return canonical?.official_pricing || routes.find((route) => route.official_pricing)?.official_pricing || null;
  }
  function renderAllProviderPriceCapV9(row, source, capRoutes) {
    const pricing = row.querySelector('.all-provider-pricing');
    if (!pricing) return;
    const percent = Math.max(0, Math.min(100, Number(source.maximum_official_price_percent ?? 100)));
    const value = pricing.querySelector('.max-price-value');
    if (value) value.textContent = `${percent}% of model official${percent < 100 ? ` (−${100 - percent}%)` : ''}`;
    const slider = pricing.querySelector('.source-max-price-percent');
    if (slider) slider.style.setProperty('--slider-percent', `${percent}%`);
    const routes = sourceRoutesForModel(source);
    const providerRows = [...row.querySelectorAll('.selected-provider-routes .route-price-line')];
    const routeUnderCap = (route) => {
      if (!route || !route.official_pricing) return false;
      const officialInput = Number(route.official_pricing.input);
      const officialOutput = Number(route.official_pricing.output);
      const currentInput = Number(route.pricing?.input);
      const currentOutput = Number(route.pricing?.output);
      if (![officialInput, officialOutput, currentInput, currentOutput].every(Number.isFinite)) return false;
      return currentInput <= officialInput * percent / 100 && currentOutput <= officialOutput * percent / 100;
    };
    routes.forEach((route, index) => {
      const providerRow = providerRows[index];
      if (!providerRow) return;
      const capped = Boolean(route?.official_pricing);
      providerRow.classList.toggle('cap-included', Boolean(capped && routeUnderCap(route)));
      providerRow.classList.toggle('cap-excluded', Boolean(capped && !routeUnderCap(route)));
      providerRow.classList.toggle('cap-not-applicable', !capped);
    });
    const includedCount = capRoutes.filter(routeUnderCap).length;
    const status = pricing.querySelector('.auction-cap-status');
    if (status) {
      status.textContent = includedCount === 0
        ? 'No providers included under this max price setting'
        : `${includedCount} provider${includedCount === 1 ? '' : 's'} included under this max price setting`;
    }
    const caps = pricing.querySelector('.auction-cap-list');
    if (!caps) return;
    caps.replaceChildren();
    // Show one provider-neutral expectation from the model's canonical
    // reference price, scaled by the selected maximum-price percentage.
    const officialPricing = modelOfficialPricing(capRoutes);
    if (officialPricing) {
      const input = Number(officialPricing.input || 0) * percent / 100;
      const output = Number(officialPricing.output || 0) * percent / 100;
      const line = document.createElement('small');
      line.className = 'auction-cap-line expected-price';
      const label = document.createElement('span');
      label.textContent = 'Max pricing';
      const values = document.createElement('strong');
      values.textContent = `${displayPrice(input)} in / ${displayPrice(output)} out`;
      line.append(label, document.createTextNode(' · '), values);
      caps.append(line);
    }
  }
  function renderSelectedSourcesV9(card) {
    const list = card.querySelector('.selected-sources');
    if (!list) return;
    list.replaceChildren();
    const kind = card.querySelector('.source-kind').value;
    card._sources = (card._sources || []).filter((source) => source.kind === kind);
    if (!card._sources.length) return;
    card._sources.forEach((source, sourceIndex) => {
      const row = document.createElement('div');
      row.className = 'selected-source';
      row.draggable = false;
      row.dataset.sourceIndex = sourceIndex;
      const handle = document.createElement('span');
      handle.className = 'drag-handle';
      handle.textContent = '☷';
      handle.draggable = true;
      handle.title = 'Drag to reorder';
      const main = document.createElement('div');
      main.className = 'selected-source-main';
      const header = document.createElement('div');
      header.className = 'selected-source-header';
      const title = document.createElement('strong');
      const model = kind === 'model' ? modelDisplay(source.model_id) : null;
      title.textContent = kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group');
      const actions = document.createElement('div');
      actions.className = 'selected-source-header-actions';
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.className = 'retry-summary';
      retry.dataset.retrySource = sourceIndex;
      retry.setAttribute('aria-label', `Configure retries, currently ${Math.max(1, Number(source.retries ?? 1))}`);
      retry.title = 'Configure retries';
      retry.innerHTML = `<span aria-hidden="true">↻</span><span>${Math.max(1, Number(source.retries ?? 1))}</span>`;
      actions.append(retry);
      const duplicate = document.createElement('button');
      duplicate.type = 'button';
      duplicate.className = 'source-action source-duplicate route-icon-button';
      duplicate.dataset.duplicateSource = sourceIndex;
      duplicate.setAttribute('aria-label', 'Duplicate provider route');
      duplicate.title = 'Duplicate provider route';
      duplicate.innerHTML = '<span aria-hidden="true">⧉</span>';
      actions.append(duplicate);
      header.append(title, actions);
      main.append(header);
      if (kind === 'group') {
        const meta = document.createElement('small');
        meta.textContent = state.groups.find((group) => group.id === source.group_id)?.slug || '';
        main.append(meta);
      } else {
        const meta = document.createElement('small');
        meta.textContent = source.model_id;
        main.append(meta);
        const routes = sourceRoutesForModel(source);
        const providerList = document.createElement('div');
        providerList.className = 'selected-provider-routes';
        routes.forEach((route) => {
          appendRoutePriceV7(providerList, route);
          providerList.lastElementChild?.querySelector('.route-price-provider')?.classList.add('selected-route-provider');
        });
        if (!routes.length) {
          const empty = document.createElement('small');
          empty.textContent = 'No current provider routes';
          providerList.append(empty);
        }
        main.append(providerList);
      }
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'source-remove route-icon-button';
      remove.dataset.removeSource = sourceIndex;
      remove.setAttribute('aria-label', 'Remove provider route');
      remove.title = 'Remove provider route';
      remove.innerHTML = '<span aria-hidden="true">×</span>';
      actions.append(remove);
      const controls = document.createElement('div');
      controls.className = 'selected-source-controls';
      if (kind === 'model' && !source.provider_name) {
        const include = document.createElement('div');
        include.className = 'include-new-providers';
        const includeLabel = document.createElement('label');
        includeLabel.className = 'include-new-toggle';
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.className = 'source-include-new';
        checkbox.checked = source.include_new_providers !== false;
        const checkmark = document.createElement('span');
        checkmark.className = 'include-checkbox-mark';
        const text = document.createElement('span');
        text.textContent = 'Include new providers';
        includeLabel.append(checkbox, checkmark, text);
        const help = document.createElement('button');
        help.type = 'button';
        help.className = 'tooltip-help';
        help.textContent = '?';
        help.setAttribute('aria-label', 'Explain include new providers');
        help.setAttribute('aria-expanded', 'false');
        help.title = 'When a new provider with this model is added, it is automatically added to this group.';
        const helpText = document.createElement('span');
        helpText.className = 'tooltip-help-text';
        helpText.hidden = true;
        helpText.textContent = help.title;
        help.addEventListener('click', (event) => {
          event.preventDefault();
          event.stopPropagation();
          const expanded = help.getAttribute('aria-expanded') === 'true';
          help.setAttribute('aria-expanded', String(!expanded));
          helpText.hidden = expanded;
        });
        include.append(includeLabel, help, helpText);
        controls.append(include);
      }
      const capRoutes = kind === 'model' ? sourceRoutesForModel(source).filter((route) => route.official_pricing) : [];
      if (capRoutes.length) {
        const pricing = document.createElement('div');
        pricing.className = 'route-setting-section all-provider-pricing';
        const heading = document.createElement('div');
        heading.className = 'route-setting-heading';
        const label = document.createElement('strong');
        label.textContent = 'Max allowed price';
        const output = document.createElement('output');
        output.className = 'max-price-value';
        heading.append(label, output);
        const slider = document.createElement('input');
        slider.type = 'range';
        slider.min = '0';
        slider.max = '100';
        slider.step = '1';
        slider.className = 'source-auction-percent source-max-price-percent';
        slider.value = source.maximum_official_price_percent ?? 100;
        const status = document.createElement('small');
        status.className = 'auction-cap-status';
        status.setAttribute('aria-live', 'polite');
        const caps = document.createElement('div');
        caps.className = 'auction-cap-list';
        pricing.append(heading, slider, status, caps);
        controls.append(pricing);
        row.dataset.hasAuctionPricing = 'true';
      }
      row.append(handle, main, controls);
      list.append(row);
      if (capRoutes.length) renderAllProviderPriceCapV9(row, source, capRoutes);
    });
  }
  renderSelectedSourcesV4 = renderSelectedSourcesV9;
  renderGroupStages = function(definition) { const container = $('#group-stage-list'); if (!container) return; container.replaceChildren(); const stages = definition.stages?.length ? definition.stages : blankGroup().stages; stages.forEach((stage, index) => container.append(stageSourceOptionsV4(stage, index))); };
  if (!window.__groupPickerOutsideClickV5) { document.addEventListener('click', (event) => { if (event.target.closest('.source-picker')) return; document.querySelectorAll('.source-search-popover:not([hidden])').forEach((popover) => closeSourcePickerV4(popover.closest('.group-stage-card'))); }); window.__groupPickerOutsideClickV5 = true; }
  // The editor below intentionally models one selected provider route per
  // source. A model is picked first; its provider routes are expanded here so
  // users can deselect individual providers and apply limits at that level.
  function providerRoutesForModel(modelId) { return state.models.filter((route) => route.model === modelId); }
  function providerNamesForModel(modelId) { return [...new Set(providerRoutesForModel(modelId).map((route) => route.provider))]; }
  function sourceProviderNames(source) {
    if (source.provider_name) return [source.provider_name];
    if (source.include_new_providers !== false) return providerNamesForModel(source.model_id);
    return source.provider_names?.length ? source.provider_names : providerNamesForModel(source.model_id);
  }
  function sourceRoutesForModel(source) {
    const allowed = new Set(sourceProviderNames(source).map((provider) => provider.toLowerCase()));
    return providerRoutesForModel(source.model_id).filter((route) => allowed.size === 0 || allowed.has(route.provider.toLowerCase()));
  }
  const renderSelectedSourcesV9Original = renderSelectedSourcesV9;
  function renderSelectedSourcesV10(card) {
    renderSelectedSourcesV9Original(card);
    card.querySelectorAll('.selected-source').forEach((row) => {
      const source = card._sources?.[Number(row.dataset.sourceIndex)];
      if (!source?.provider_name) return;
      const route = routeForSource(source);
      if (routeBilling(route) !== 'subscription') return;
      // Subscription routes are intentionally single-provider blocks. They
      // do not participate in the model-wide provider pool or its price cap.
      row.querySelector('.include-new-providers')?.remove();
      row.querySelector('.all-provider-pricing')?.remove();
      row.querySelector('.selected-provider-routes')?.remove();
      const controls = row.querySelector('.selected-source-controls');
      if (!controls || controls.querySelector('.subscription-warning')) return;
      const warning = document.createElement('small');
      warning.className = 'subscription-warning';
      warning.textContent = 'Subscription plan · may be rate limited';
      controls.prepend(warning);
    });
  }
  renderSelectedSourcesV4 = renderSelectedSourcesV10;
  function compactRouteSummaryV11(container, route) {
    if (!route) return;
    const line = document.createElement('div');
    line.className = 'selected-route-summary compact-route-summary';
    const access = routeAccessLabelV4(route);
    if (access) {
      const accessNode = document.createElement('span');
      accessNode.className = `route-access-label ${access.toLowerCase()}`;
      accessNode.textContent = access;
      line.append(accessNode);
    }
    const prices = document.createElement('span');
    prices.className = 'route-price-values';
    prices.textContent = `${displayPrice(route.pricing?.input)} in / ${displayPrice(route.pricing?.output)} out`;
    line.append(prices);
    const discount = routeDiscountInfoV7(route);
    if (discount) {
      const badge = document.createElement('strong');
      badge.className = `route-discount${discount.label === 'Official price' ? ' official' : ''}`;
      badge.textContent = discount.label;
      line.append(badge);
    }
    container.append(line);
  }
  function renderStagePriceCapV11(card) {
    card.querySelector('.stage-price-cap')?.remove();
    const routes = (card._sources || [])
      .filter((source) => source.kind === 'model')
      .flatMap((source) => sourceRoutesForModel(source))
      .filter((route) => route?.official_pricing && routeBilling(route) !== 'subscription')
      .filter((route, index, all) => all.findIndex((candidate) => candidate.model === route.model && candidate.provider === route.provider) === index);
    card.querySelectorAll('.selected-source').forEach((row) => {
      const source = card._sources?.[Number(row.dataset.sourceIndex)];
      const route = source?.provider_name ? routeForSource(source) : null;
      row.classList.remove('cap-included', 'cap-excluded', 'cap-not-applicable');
      if (!route?.official_pricing || routeBilling(route) === 'subscription') {
        row.classList.add('cap-not-applicable');
        return;
      }
      const percent = Math.max(0, Math.min(100, Number(source.maximum_official_price_percent ?? 100)));
      const under = Number(route.pricing?.input) <= Number(route.official_pricing.input) * percent / 100 && Number(route.pricing?.output) <= Number(route.official_pricing.output) * percent / 100;
      row.classList.add(under ? 'cap-included' : 'cap-excluded');
    });
    if (!routes.length) return;
    const percent = Math.max(0, Math.min(100, Number((card._sources || []).find((source) => source.maximum_official_price_percent != null)?.maximum_official_price_percent ?? 100)));
    const panel = document.createElement('div');
    panel.className = 'route-setting-section stage-price-cap all-provider-pricing';
    const heading = document.createElement('div');
    heading.className = 'route-setting-heading';
    const label = document.createElement('strong');
    label.textContent = 'Max allowed price';
    const output = document.createElement('output');
    output.className = 'max-price-value';
    output.textContent = `${percent}% of model official${percent < 100 ? ` (−${100 - percent}%)` : ''}`;
    heading.append(label, output);
    const slider = document.createElement('input');
    slider.type = 'range';
    slider.min = '0';
    slider.max = '100';
    slider.step = '1';
    slider.className = 'stage-max-price-percent';
    slider.value = percent;
    slider.style.setProperty('--slider-percent', `${percent}%`);
    const status = document.createElement('small');
    status.className = 'auction-cap-status';
    const included = routes.filter((route) => Number(route.pricing?.input) <= Number(route.official_pricing.input) * percent / 100 && Number(route.pricing?.output) <= Number(route.official_pricing.output) * percent / 100).length;
    status.textContent = `${included} provider${included === 1 ? '' : 's'} included under this max price setting`;
    const official = modelOfficialPricing(routes);
    if (official) {
      const cap = document.createElement('small');
      cap.className = 'auction-cap-line expected-price';
      cap.textContent = `Max pricing · ${displayPrice(Number(official.input || 0) * percent / 100)} in / ${displayPrice(Number(official.output || 0) * percent / 100)} out`;
      panel.append(heading, slider, status, cap);
    } else panel.append(heading, slider, status);
    card.querySelector('.selected-sources')?.after(panel);
  }
  function updateStagePriceCapV11(card, percent) {
    const panel = card.querySelector('.stage-price-cap');
    if (!panel) return;
    const value = panel.querySelector('.max-price-value');
    if (value) value.textContent = `${percent}% of model official${percent < 100 ? ` (−${100 - percent}%)` : ''}`;
    const slider = panel.querySelector('.stage-max-price-percent');
    if (slider) slider.style.setProperty('--slider-percent', `${percent}%`);
    const routes = (card._sources || [])
      .filter((source) => source.kind === 'model')
      .flatMap((source) => sourceRoutesForModel(source))
      .filter((route) => route?.official_pricing && routeBilling(route) !== 'subscription')
      .filter((route, index, all) => all.findIndex((candidate) => candidate.model === route.model && candidate.provider === route.provider) === index);
    const included = routes.filter((route) => Number(route.pricing?.input) <= Number(route.official_pricing.input) * percent / 100 && Number(route.pricing?.output) <= Number(route.official_pricing.output) * percent / 100).length;
    const status = panel.querySelector('.auction-cap-status');
    if (status) status.textContent = `${included} provider${included === 1 ? '' : 's'} included under this max price setting`;
    const official = modelOfficialPricing(routes);
    const cap = panel.querySelector('.auction-cap-line');
    if (cap && official) cap.textContent = `Max pricing · ${displayPrice(Number(official.input || 0) * percent / 100)} in / ${displayPrice(Number(official.output || 0) * percent / 100)} out`;
    card.querySelectorAll('.selected-source').forEach((row) => {
      const source = card._sources?.[Number(row.dataset.sourceIndex)];
      const route = source?.provider_name ? routeForSource(source) : null;
      row.classList.remove('cap-included', 'cap-excluded', 'cap-not-applicable');
      if (!route?.official_pricing || routeBilling(route) === 'subscription') row.classList.add('cap-not-applicable');
      else row.classList.add(Number(route.pricing?.input) <= Number(route.official_pricing.input) * percent / 100 && Number(route.pricing?.output) <= Number(route.official_pricing.output) * percent / 100 ? 'cap-included' : 'cap-excluded');
    });
  }
  function renderSelectedSourcesV11(card) {
    const list = card.querySelector('.selected-sources');
    if (!list) return;
    list.replaceChildren();
    card._sources = (card._sources || []).filter((source) => source.kind === card.querySelector('.source-kind').value);
    card._sources.forEach((source, sourceIndex) => {
      const row = document.createElement('div');
      row.className = 'selected-source compact-provider-source';
      row.draggable = false;
      row.dataset.sourceIndex = sourceIndex;
      const handle = document.createElement('span');
      handle.className = 'drag-handle';
      handle.textContent = '☷';
      handle.draggable = true;
      handle.title = 'Drag to reorder';
      const main = document.createElement('div');
      main.className = 'selected-source-main';
      const header = document.createElement('div');
      header.className = 'selected-source-header';
      const title = document.createElement('strong');
      const model = source.kind === 'model' ? modelDisplay(source.model_id) : null;
      title.textContent = source.kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group');
      const titleWrap = document.createElement('div');
      titleWrap.className = 'selected-source-title';
      titleWrap.append(title);
      if (source.kind === 'group') {
        const badge = document.createElement('span');
        badge.className = 'selected-source-type';
        badge.textContent = 'GROUP';
        titleWrap.append(badge);
      }
      const actions = document.createElement('div');
      actions.className = 'selected-source-header-actions';
      const futureProvider = source.kind === 'model' && !source.provider_name;
      const route = source.kind === 'model' && source.provider_name ? routeForSource(source) : null;
      if (route && routeBilling(route) === 'subscription') row.classList.add('subscription-route');
      if (futureProvider) row.classList.add(source.include_new_providers !== false ? 'new-provider-enabled' : 'new-provider-disabled');
      if (!futureProvider) {
        const retry = document.createElement('button');
        retry.type = 'button';
        retry.className = 'retry-summary';
        retry.dataset.retrySource = sourceIndex;
        retry.setAttribute('aria-label', `Configure retries, currently ${Math.max(1, Number(source.retries ?? 1))}`);
        retry.title = 'Configure retries';
        retry.innerHTML = `<span aria-hidden="true">↻</span><span>${Math.max(1, Number(source.retries ?? 1))}</span>`;
        actions.append(retry);
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'source-remove route-icon-button';
        remove.dataset.removeSource = sourceIndex;
        remove.setAttribute('aria-label', 'Remove provider route');
        remove.title = 'Remove provider route';
        remove.innerHTML = '<span aria-hidden="true">×</span>';
        actions.append(remove);
      }
      header.append(titleWrap, actions);
      main.append(header);
      if (source.kind === 'group') {
        const meta = document.createElement('small');
        meta.textContent = state.groups.find((group) => group.id === source.group_id)?.slug || '';
        main.append(meta);
      } else {
        const provider = document.createElement('strong');
        provider.className = 'selected-route-provider';
        provider.textContent = source.provider_name ? providerName(source.provider_name) : 'New providers';
        main.append(provider);
        if (route && routeBilling(route) === 'subscription') {
          const warning = document.createElement('small');
          warning.className = 'subscription-warning';
          warning.textContent = 'Subscription plan · may be rate limited';
          main.append(warning);
        } else if (route) compactRouteSummaryV11(main, route);
        if (!source.provider_name) {
          const include = document.createElement('div');
          include.className = 'include-new-providers';
          const label = document.createElement('label');
          label.className = 'include-new-toggle';
          const checkbox = document.createElement('input');
          checkbox.type = 'checkbox';
          checkbox.className = 'source-include-new';
          checkbox.checked = source.include_new_providers !== false;
          const mark = document.createElement('span');
          mark.className = 'include-checkbox-mark';
          label.setAttribute('aria-label', `Include new providers for ${model.name}`);
          label.title = `Include new providers for ${model.name}`;
          label.append(checkbox, mark);
          include.classList.add('header-include-toggle');
          const explanation = document.createElement('small');
          explanation.className = 'new-provider-explanation';
          explanation.textContent = `When a new provider with ${model.name} is added, it is automatically added to this group.`;
          main.append(explanation);
          include.append(label);
          actions.append(include);
        }
      }
      row.append(handle, main);
      list.append(row);
    });
    renderStagePriceCapV11(card);
  }
  renderSelectedSourcesV4 = renderSelectedSourcesV11;
  // Keep the global block price cap interactive without replacing the range
  // input while the pointer is dragging.
  document.addEventListener('input', (event) => {
    const slider = event.target.closest('.stage-max-price-percent');
    if (!slider) return;
    const card = slider.closest('.group-stage-card');
    if (!card) return;
    const percent = Math.max(0, Math.min(100, Number(slider.value || 0)));
    card._sources?.forEach((source) => {
      if (source.kind !== 'model') return;
      const route = source.provider_name ? routeForSource(source) : null;
      if (!route || routeBilling(route) !== 'subscription') source.maximum_official_price_percent = percent;
    });
    updateStagePriceCapV11(card, percent);
    previewCurrentGroup();
  }, true);
  function allProviderSource(modelId) {
    const providers = [...new Set(providerRoutesForModel(modelId).filter((route) => routeBilling(route) !== 'subscription').map((route) => route.provider))];
    return { kind: 'model', model_id: modelId, provider_names: providers, include_new_providers: true, retries: 1 };
  }
  function expandProviderSources(sources) {
    return (sources || []).flatMap((source) => {
      if (source.kind !== 'model' || source.provider_name || !source.provider_names?.length) return [{ ...source }];
      const expanded = source.provider_names.map((provider) => ({ ...source, provider_name: provider, provider_names: undefined, include_new_providers: false }));
      if (source.include_new_providers !== false) expanded.push({ kind: 'model', model_id: source.model_id, include_new_providers: true, retries: source.retries });
      return expanded;
    });
  }
  function modelDisplay(modelId) { return modelCandidates().find((model) => model.id === modelId) || { id: modelId, name: modelId, routes: [] }; }
  function routeForSource(source) { return providerRoutesForModel(source.model_id).find((route) => !source.provider_name || route.provider === source.provider_name) || null; }
  function routePriceText(route) { if (!route) return 'No provider route discovered'; const currentInput = displayPrice(route.pricing?.input); const currentOutput = displayPrice(route.pricing?.output); const officialInput = displayPrice(route.official_pricing?.input); const officialOutput = displayPrice(route.official_pricing?.output); const discount = route.discount_percent_bps == null ? '' : ` · ${discountPercent(route.discount_percent_bps)} below official`; if (route.provider === 'surplus' && route.official_pricing) return `${providerName(route.provider)} · ${routeBilling(route).toUpperCase()} · ${currentInput} in / ${currentOutput} out · official ${officialInput} / ${officialOutput}${discount}`; return `${providerName(route.provider)} · ${route.free ? 'FREE' : routeBilling(route).toUpperCase()} · ${currentInput} in / ${currentOutput} out`; }
  function renderCandidatesV2(card) { const list = card.querySelector('.source-candidates'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; const query = (card.querySelector('.source-search')?.value || '').trim().toLowerCase(); if (kind === 'group') { state.groups.filter((group) => group.id !== $('#group-id').value && `${group.name} ${group.slug}`.toLowerCase().includes(query)).forEach((group) => { const item = document.createElement('div'); item.className = 'source-candidate'; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = group.name; const small = document.createElement('small'); small.textContent = group.slug; main.append(strong, small); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addGroup = group.id; add.textContent = 'Add group'; item.append(main, add); list.append(item); }); } else { modelCandidates().filter((model) => `${model.name} ${model.id} ${model.routes.map((route) => route.provider).join(' ')}`.toLowerCase().includes(query)).forEach((model) => { const item = document.createElement('div'); item.className = 'source-candidate'; const selected = (card._sources || []).filter((source) => source.kind === 'model' && source.model_id === model.id).length; if (selected) item.classList.add('selected'); item.dataset.modelId = model.id; const main = document.createElement('div'); main.className = 'source-candidate-main'; const strong = document.createElement('strong'); strong.textContent = model.name; const small = document.createElement('small'); small.textContent = model.id; main.append(strong, small); const routes = document.createElement('div'); routes.className = 'source-route-list'; model.routes.forEach((route) => { const chip = document.createElement('span'); chip.className = `source-route-chip ${routeBilling(route)}`; chip.textContent = routePriceText(route); routes.append(chip); }); main.append(routes); const add = document.createElement('button'); add.type = 'button'; add.className = 'candidate-add'; add.dataset.addModel = model.id; add.textContent = selected ? 'Add another set' : 'Add all routes'; item.append(main, add); list.append(item); }); } if (!list.children.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = 'No matching models or groups.'; list.append(empty); } }
  function appendAuctionInfo(main, source, route) { if (!route || route.provider !== 'surplus' || !route.official_pricing) return; const percent = Number(source.maximum_official_price_percent ?? 100); const input = Math.round(Number(route.official_pricing.input || 0) * percent / 100); const output = Math.round(Number(route.official_pricing.output || 0) * percent / 100); const note = document.createElement('small'); note.className = 'auction-derived'; note.textContent = `Max ${percent}% of official · ${displayPrice(input)} in / ${displayPrice(output)} out · ${100 - percent}% discount cap`; main.append(note); }
  function renderSelectedSourcesV2(card) { const list = card.querySelector('.selected-sources'); if (!list) return; list.replaceChildren(); const kind = card.querySelector('.source-kind').value; card._sources = (card._sources || []).filter((source) => source.kind === kind); if (!card._sources.length) { const empty = document.createElement('small'); empty.className = 'source-empty'; empty.textContent = kind === 'model' ? 'Choose a model above to select all of its provider routes.' : 'Choose a group above.'; list.append(empty); return; } card._sources.forEach((source, sourceIndex) => { const row = document.createElement('div'); row.className = 'selected-source'; row.draggable = true; row.dataset.sourceIndex = sourceIndex; const handle = document.createElement('span'); handle.className = 'drag-handle'; handle.textContent = '☷'; const main = document.createElement('div'); main.className = 'selected-source-main'; const model = kind === 'model' ? modelDisplay(source.model_id) : null; const title = document.createElement('strong'); title.textContent = kind === 'model' ? model.name : (state.groups.find((group) => group.id === source.group_id)?.name || source.group_id || 'Choose a group'); const meta = document.createElement('small'); meta.textContent = kind === 'model' ? `${source.model_id} · ${providerName(source.provider_name || 'all providers')}` : (state.groups.find((group) => group.id === source.group_id)?.slug || ''); main.append(title, meta); const route = kind === 'model' ? routeForSource(source) : null; if (kind === 'model') { const price = document.createElement('small'); price.className = 'selected-route-price'; price.textContent = routePriceText(route); main.append(price); appendAuctionInfo(main, source, route); } const controls = document.createElement('div'); controls.className = 'selected-source-controls'; if (kind === 'model') { const providerLabel = document.createElement('label'); providerLabel.textContent = 'Provider route'; const provider = document.createElement('select'); provider.className = 'source-provider'; provider.innerHTML = providerNamesForModel(source.model_id).map((value) => `<option value="${value}">${providerName(value)}</option>`).join(''); provider.value = source.provider_name || ''; providerLabel.append(provider); controls.append(providerLabel); if (route?.provider === 'surplus' && route.official_pricing) { const auctionLabel = document.createElement('label'); auctionLabel.textContent = 'Max official %'; const auction = document.createElement('input'); auction.type = 'number'; auction.min = '0'; auction.max = '100'; auction.step = '1'; auction.className = 'source-auction-percent'; auction.value = source.maximum_official_price_percent ?? 100; auctionLabel.append(auction); controls.append(auctionLabel); } } const retryLabel = document.createElement('label'); retryLabel.textContent = 'Retries'; const retry = document.createElement('input'); retry.type = 'number'; retry.min = '0'; retry.max = '5'; retry.step = '1'; retry.className = 'source-retries'; retry.value = source.retries ?? 1; retryLabel.append(retry); controls.append(retryLabel); const duplicate = document.createElement('button'); duplicate.type = 'button'; duplicate.className = 'source-action'; duplicate.dataset.duplicateSource = sourceIndex; duplicate.textContent = 'Duplicate'; controls.append(duplicate); const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'source-action'; remove.dataset.removeSource = sourceIndex; remove.textContent = 'Deselect'; controls.append(remove); row.append(handle, main, controls); list.append(row); }); }
  function stageSourceOptions(stage, index) { const card = document.createElement('article'); card.className = 'group-stage-card'; card.dataset.stageIndex = index; const rawSources = (stage.sources || []).filter((source) => source.model_id || source.group_id).map((source) => ({ ...source, kind: source.kind || (source.group_id ? 'group' : 'model') })); card._sources = expandProviderSources(rawSources); const kind = card._sources[0]?.kind || 'model'; card.innerHTML = `<div class="stage-card-heading"><strong>TRY ${index + 1}</strong><button type="button" class="quiet-button remove-stage">Remove</button></div><label>Try name<input class="stage-name" maxlength="120"></label><div class="try-settings"><label>Retry this try<input class="try-retries" type="number" min="0" max="5" step="1"></label><small>Repeats the complete candidate block before moving to the next try.</small></div><label>Source type<select class="source-kind"><option value="model">Models</option><option value="group">Another group</option></select></label><div class="source-picker"><input class="source-search" type="search" placeholder="Find a model by name, provider, or ID…" autocomplete="off"><div class="source-candidates"></div></div><div class="selected-route-heading"><strong>Selected provider routes</strong><small>All routes are selected when you add a model. Deselect individual providers below.</small></div><div class="selected-sources"></div><label class="stage-billing">Access <span><label><input type="checkbox" value="free"> Free</label><label><input type="checkbox" value="subscription"> Subscription</label><label><input type="checkbox" value="metered"> Metered API</label></span></label><div class="stage-limit-grid"><label>Max input $ / 1M<input class="stage-input-limit" type="number" min="0" step="0.000001" placeholder="No limit"></label><label>Max output $ / 1M<input class="stage-output-limit" type="number" min="0" step="0.000001" placeholder="No limit"></label><label>Max expected $ / request<input class="stage-total-limit" type="number" min="0" step="0.000001" placeholder="No limit"></label></div><small class="stage-explanation">Routes run in the order shown. Drag to reorder; duplicate a route for a separate auction cap.</small>`; card.querySelector('.stage-name').value = stage.name || ''; card.querySelector('.source-kind').value = kind; card.querySelectorAll('.stage-billing input').forEach((input) => { input.checked = (stage.billing_classes || ['free', 'subscription', 'metered']).includes(input.value); }); card.querySelector('.stage-input-limit').value = picoPerTokenToUSDPerMillion(stage.maximum_input_pico_usd_per_token); card.querySelector('.stage-output-limit').value = picoPerTokenToUSDPerMillion(stage.maximum_output_pico_usd_per_token); card.querySelector('.stage-total-limit').value = picoToUSD(stage.maximum_expected_cost_pico_usd); card.querySelector('.try-retries').value = stage.try_retries ?? 1; card.querySelector('.source-kind').addEventListener('change', () => { card._sources = []; renderSelectedSourcesV2(card); renderCandidatesV2(card); previewCurrentGroup(); }); card.querySelector('.source-search').addEventListener('input', () => renderCandidatesV2(card)); card.addEventListener('click', (event) => { const addModel = event.target.closest('[data-add-model]'); const addGroup = event.target.closest('[data-add-group]'); if (addModel) { const providers = providerNamesForModel(addModel.dataset.addModel); if (providers.length) providers.forEach((provider) => card._sources.push({ kind: 'model', model_id: addModel.dataset.addModel, provider_name: provider, retries: 1 })); else card._sources.push({ kind: 'model', model_id: addModel.dataset.addModel, retries: 1 }); renderSelectedSourcesV2(card); renderCandidatesV2(card); previewCurrentGroup(); return; } if (addGroup) { card._sources = [{ kind: 'group', group_id: addGroup.dataset.addGroup, retries: 1 }]; renderSelectedSourcesV2(card); renderCandidatesV2(card); previewCurrentGroup(); return; } const duplicate = event.target.closest('[data-duplicate-source]'); if (duplicate) { const source = card._sources[Number(duplicate.dataset.duplicateSource)]; if (source) card._sources.splice(Number(duplicate.dataset.duplicateSource) + 1, 0, { ...source }); renderSelectedSourcesV2(card); previewCurrentGroup(); return; } const remove = event.target.closest('[data-remove-source]'); if (remove) { card._sources.splice(Number(remove.dataset.removeSource), 1); renderSelectedSourcesV2(card); renderCandidatesV2(card); previewCurrentGroup(); } }); card.addEventListener('input', (event) => { const row = event.target.closest('.selected-source'); if (!row) return; const source = card._sources[Number(row.dataset.sourceIndex)]; if (!source) return; if (event.target.classList.contains('source-retries')) source.retries = Number(event.target.value || 0); if (event.target.classList.contains('source-auction-percent')) source.maximum_official_price_percent = Number(event.target.value || 0); renderSelectedSourcesV2(card); }); card.addEventListener('change', (event) => { if (event.target.classList.contains('source-provider')) { const row = event.target.closest('.selected-source'); const source = card._sources[Number(row.dataset.sourceIndex)]; if (source) source.provider_name = event.target.value; renderSelectedSourcesV2(card); previewCurrentGroup(); } }); card.addEventListener('dragstart', (event) => { const row = event.target.closest('.selected-source'); if (row) { event.dataTransfer.setData('text/plain', row.dataset.sourceIndex); row.classList.add('dragging'); } }); card.addEventListener('dragend', (event) => event.target.closest('.selected-source')?.classList.remove('dragging')); card.addEventListener('dragover', (event) => { if (event.target.closest('.selected-source')) event.preventDefault(); }); card.addEventListener('drop', (event) => { const target = event.target.closest('.selected-source'); if (!target) return; event.preventDefault(); const from = Number(event.dataTransfer.getData('text/plain')); const to = Number(target.dataset.sourceIndex); if (from === to || Number.isNaN(from) || Number.isNaN(to)) return; const moved = card._sources.splice(from, 1)[0]; card._sources.splice(to, 0, moved); renderSelectedSourcesV2(card); previewCurrentGroup(); }); renderSelectedSourcesV2(card); renderCandidatesV2(card); return card; }
  function renderGroupStages(definition) { const container = $('#group-stage-list'); if (!container) return; container.replaceChildren(); const stages = definition.stages?.length ? definition.stages : blankGroup().stages; stages.forEach((stage, index) => container.append(stageSourceOptions(stage, index))); }
  function collectGroupDefinition() { const stages = $$('#group-stage-list .group-stage-card').map((card, index) => { const stage = { position: index, name: card.querySelector('.stage-name').value, sources: (card._sources || []).map((source) => ({ ...source })), billing_classes: [...card.querySelectorAll('.stage-billing input:checked')].map((input) => input.value), selection: 'lowest_expected_cost', try_retries: Number(card.querySelector('.try-retries').value || 0) }; const inputLimit = usdPerMillionToPico(card.querySelector('.stage-input-limit').value); const outputLimit = usdPerMillionToPico(card.querySelector('.stage-output-limit').value); const totalLimit = usdToPico(card.querySelector('.stage-total-limit').value); if (inputLimit !== undefined) stage.maximum_input_pico_usd_per_token = inputLimit; if (outputLimit !== undefined) stage.maximum_output_pico_usd_per_token = outputLimit; if (totalLimit !== undefined) stage.maximum_expected_cost_pico_usd = totalLimit; return stage; }); return { id: $('#group-id').value || undefined, revision: Number($('#group-revision').value || 0), name: $('#group-name').value, slug: $('#group-slug').value, description: $('#group-description').value, enabled: $('#group-enabled').checked, stages }; }
  function setGroupFeedback(message, kind = 'warning') { const node = $('#group-feedback'); if (!node) return; node.hidden = !message; node.className = `provider-feedback ${kind}`; node.textContent = message || ''; }
  function openGroupEditor(definition = blankGroup()) { const editor = $('#group-editor'); if (!editor) return; $('#group-id').value = definition.id || ''; $('#group-revision').value = definition.revision || 0; $('#group-editor-title').textContent = definition.id ? 'Edit group' : 'Create group'; $('#group-name').value = definition.name || ''; $('#group-slug').value = definition.slug || ''; $('#group-description').value = definition.description || ''; $('#group-enabled').checked = definition.enabled !== false; renderGroupStages(definition); setGroupFeedback(''); editor.hidden = false; editor.scrollIntoView({ behavior: 'smooth', block: 'start' }); }
  async function loadGroups() { try { state.groups = (await fetchJSON('/api/groups')).data || []; renderGroups(); } catch (_) { setText('#groups-list', 'Unable to load groups'); } }
  async function previewCurrentGroup() { const node = $('#group-preview'); if (!node || $('#group-editor')?.hidden) return; try { const payload = await fetchJSON('/api/groups/preview', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(collectGroupDefinition()) }); const errors = (payload.issues || []).filter((item) => item.level === 'error'); const planError = payload.plan?.error; node.textContent = errors.length ? `Blocked · ${errors[0].message}` : planError ? `Blocked · ${planError.message}` : `Ready · ${(payload.plan?.entries || []).length} current routes`; } catch (error) { node.textContent = error.message; } }

  function openModal(id) { const modal = $(`#${id}`); if (!modal) return; modal.hidden = false; document.body.classList.add('modal-open'); setTimeout(() => modal.querySelector('input, select')?.focus(), 0); }
  function closeModal(modal) { const element = typeof modal === 'string' ? $(`#${modal}`) : modal; if (element) element.hidden = true; if (!$$('.modal-backdrop:not([hidden])').length) document.body.classList.remove('modal-open'); }
  function navigate(view) { const valid = ['overview', 'models', 'groups', 'stats', 'requests', 'access', 'settings']; state.view = valid.includes(view) ? view : 'overview'; $$('.view-panel').forEach((panel) => panel.classList.toggle('active', panel.dataset.viewPanel === state.view)); $$('.nav-item').forEach((item) => item.classList.toggle('active', item.dataset.view === state.view)); const meta = { overview: ['Overview', 'Your local routing desk at a glance.'], models: ['Models', 'Search provider routes and compare live pricing.'], groups: ['Groups', 'Callable aliases with ordered routing rules and fallbacks.'], stats: ['Statistics', 'Reliability, retries, response time, and cost by group, provider, and model.'], requests: ['Requests', 'Detailed usage, cost, and request outcomes.'], access: ['Access & keys', 'Manage the keys and providers behind your proxy.'], settings: ['Settings', 'Configure self-updating and inspect version history.'] }[state.view]; setText('#page-title', meta[0]); setText('#page-kicker', meta[0].toUpperCase()); setText('#page-description', meta[1]); $('#sidebar').classList.remove('open'); if (window.location.hash !== `#${state.view}`) history.replaceState(null, '', `#${state.view}`); }
  function showGroupInstructions(group) {
    const baseURL = `${window.location.origin}/v1`;
    setText('#group-instructions-name', group.name);
    setText('#group-instructions-base-url', baseURL);
    setText('#group-instructions-model', group.slug);
    const curl = [`curl ${baseURL}/chat/completions`, '  -H "Authorization: Bearer <your-client-key>"', '  -H "Content-Type: application/json"', `  -d '{"model":"${group.slug}","messages":[{"role":"user","content":"Hello"}]}'`].join('\n');
    setText('#group-instructions-curl', curl);
    openModal('group-instructions-modal');
  }
  function navigate(view) { const valid = ['overview', 'models', 'groups', 'stats', 'requests', 'access', 'settings']; state.view = valid.includes(view) ? view : 'overview'; $$('.view-panel').forEach((panel) => panel.classList.toggle('active', panel.dataset.viewPanel === state.view)); $$('.nav-item').forEach((item) => item.classList.toggle('active', item.dataset.view === state.view)); const meta = { overview: ['Overview', 'Your local routing desk at a glance.'], models: ['Models', 'Search provider routes and compare live pricing.'], groups: ['Groups', 'Callable aliases with ordered routing rules and fallbacks.'], stats: ['Statistics', 'Reliability, retries, response time, and cost by group, provider, and model.'], requests: ['Requests', 'Detailed usage, cost, and request outcomes.'], access: ['Access & keys', 'Manage the keys and providers behind your proxy.'], settings: ['Settings', 'Configure the local listener and application defaults.'] }[state.view]; setText('#page-title', meta[0]); setText('#page-kicker', meta[0].toUpperCase()); setText('#page-description', meta[1]); $('#sidebar').classList.remove('open'); if (window.location.hash !== `#${state.view}`) history.replaceState(null, '', `#${state.view}`); }

  $('#key-form').addEventListener('submit', async (event) => { event.preventDefault(); try { const payload = await fetchJSON('/api/client-keys', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ label: $('#key-label').value }) }); const secret = $('#new-key'); secret.hidden = false; secret.textContent = `Copy this key now — it will not be shown again: ${payload.secret}`; $('#key-form').reset(); await loadKeys(); } catch (error) { setText('#new-key', error.message); $('#new-key').hidden = false; } });
  $('#retry-form').addEventListener('submit', (event) => { event.preventDefault(); const target = state.retryTarget; const input = $('#retry-count'); if (!target || !input) return; const source = target.card?._sources?.[target.sourceIndex]; if (!source) return; source.retries = Math.max(1, Math.min(5, Number(input.value || 1))); renderSelectedSourcesV4(target.card); previewCurrentGroup(); closeModal('retry-modal'); state.retryTarget = null; });
  function ensureSubscriptionFields() { const form = $('#provider-form'); if (!form || $('#provider-access-mode')) return; const label = document.createElement('label'); label.htmlFor = 'provider-access-mode'; label.textContent = 'Access type'; const select = document.createElement('select'); select.id = 'provider-access-mode'; select.innerHTML = '<option value="api">API usage</option><option value="subscription">Subscription plan</option>'; const feeWrap = document.createElement('div'); feeWrap.id = 'subscription-fields'; feeWrap.hidden = true; feeWrap.innerHTML = '<label for="subscription-fee">Subscription fee (USD / billing cycle)</label><input id="subscription-fee" type="number" min="0.01" step="0.01" placeholder="20.00">'; form.insertBefore(label, form.querySelector('#provider-progress')); form.insertBefore(select, form.querySelector('#provider-progress')); form.insertBefore(feeWrap, form.querySelector('#provider-progress')); select.addEventListener('change', updateProviderFormMode); }
  function setProviderSaving(saving) { const form = $('#provider-form'); const progress = $('#provider-progress'); if (!form) return; form.setAttribute('aria-busy', saving ? 'true' : 'false'); form.querySelectorAll('input, select, textarea, button[type="submit"]').forEach((control) => { control.disabled = saving; }); if (progress) progress.hidden = !saving; }
  $('#provider-form').addEventListener('submit', async (event) => { event.preventDefault(); const custom = $('#provider-type').value === 'custom'; let manualModels = []; try { manualModels = parseManualModels(); } catch (error) { setProviderFeedback(error.message, 'error'); return; } setProviderFeedback(''); setProviderSaving(true); try { const payload = await fetchJSON('/api/providers/credentials', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ provider: custom ? $('#provider-name').value : $('#provider-type').value, base_url: custom ? $('#provider-base-url').value : '', label: $('#provider-label').value, api_key: $('#provider-key').value, manual_models: manualModels }) }); const found = Number(payload.models_found ?? Number(payload.models_discovered || 0) + Number(payload.models_verified || 0)); $('#provider-form').reset(); updateProviderFormMode(); $('#manual-model-fields').hidden = true; setProviderSaving(false); setProviderFeedback(`Found ${formatNumber(found)} model${found === 1 ? '' : 's'}. Provider verified and saved.`, 'success'); await Promise.all([loadProviders(), loadModels(), loadStatus()]); await new Promise((resolve) => setTimeout(resolve, 1200)); closeModal('provider-modal'); setProviderFeedback(''); } catch (error) { const details = error.payload?.error; if (details?.can_enter_models) { $('#manual-model-fields').hidden = false; setProviderFeedback(`${details.message} Enter model IDs and prices below; each will be verified before the credential is saved.`); } else { setProviderFeedback(error.message, 'error'); } } finally { setProviderSaving(false); } });
  $('#network-settings-form').addEventListener('submit', async (event) => { event.preventDefault(); setNetworkFeedback(''); const port = Number($('#network-port').value); try { const data = await fetchJSON('/api/settings/network', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ port }) }); state.network = data; await loadNetworkSettings(); setNetworkFeedback(data.restart_required ? 'Port saved. Restart PayLessForAI to apply it.' : 'Port saved and active.', 'success'); } catch (error) { setNetworkFeedback(error.message, 'error'); } });
  $('#open-key-modal').addEventListener('click', () => openModal('key-modal')); $('#open-provider-modal').addEventListener('click', () => { $('#provider-form').reset(); updateProviderFormMode(); $('#manual-model-fields').hidden = true; setProviderFeedback(''); openModal('provider-modal'); }); $('#provider-type').addEventListener('change', updateProviderFormMode); $('#menu-toggle').addEventListener('click', () => $('#sidebar').classList.toggle('open')); $('#refresh-button').addEventListener('click', () => Promise.all([loadStatus(), loadSummary(), loadRequests(), loadModelStats(), loadProviderStats(), loadGroupStats(), loadModels(), loadKeys(), loadProviders(), loadGroups(), loadNetworkSettings()])); $('#models-refresh').addEventListener('click', loadModels);
  $('#models-search').addEventListener('input', renderModels); $('#models-clear-filters').addEventListener('click', clearModelFilters); $$('.table-sort').forEach((button) => button.addEventListener('click', () => cycleModelSort(button.dataset.sortKey))); $$('.table-filter').forEach((button) => button.addEventListener('click', (event) => { event.stopPropagation(); openModelFilter(button.dataset.filterKey, button); })); $('#requests-search').addEventListener('input', renderRequestTable); $('#requests-state').addEventListener('change', renderRequestTable); $('#groups-search').addEventListener('input', renderGroups); $('#open-group-editor').addEventListener('click', () => openGroupEditor()); $('#close-group-editor').addEventListener('click', () => { $('#group-editor').hidden = true; }); $('#cancel-group').addEventListener('click', () => { $('#group-editor').hidden = true; }); $('#add-group-stage').addEventListener('click', () => { const current = collectGroupDefinition(); current.stages.push({ position: current.stages.length, name: `Fallback ${current.stages.length + 1}`, sources: [], billing_classes: ['free', 'subscription', 'metered'], selection: 'lowest_expected_cost', try_retries: 1 }); renderGroupStages(current); previewCurrentGroup(); }); $('#group-stage-list').addEventListener('change', previewCurrentGroup); $('#group-stage-list').addEventListener('input', previewCurrentGroup); $('#group-form').addEventListener('submit', async (event) => { event.preventDefault(); setGroupFeedback(''); const definition = collectGroupDefinition(); const id = definition.id; const url = id ? `/api/groups/${encodeURIComponent(id)}?revision=${encodeURIComponent(definition.revision)}` : '/api/groups'; try { const payload = await fetchJSON(url, { method: id ? 'PUT' : 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(definition) }); openGroupEditor(payload.data); setGroupFeedback('Group saved and ready to call.', 'success'); await Promise.all([loadGroups(), loadModels()]); } catch (error) { const details = error.payload?.error; setGroupFeedback(details?.issues?.[0]?.message || error.message, 'error'); } });
  document.addEventListener('click', (event) => { if (state.filterPopover && !event.target.closest('#models-filter-popover') && !event.target.closest('.table-filter')) closeModelFilter(); const target = event.target.closest('[data-view]'); if (target) { event.preventDefault(); navigate(target.dataset.view); } const requestTarget = event.target.closest('[data-request-id]'); if (requestTarget) showRequestDetail(requestTarget.dataset.requestId); const connectGroup = event.target.closest('[data-connect-group]'); if (connectGroup) { const group = state.groups.find((item) => item.id === connectGroup.dataset.connectGroup); if (group) showGroupInstructions(group); return; } const editGroup = event.target.closest('[data-edit-group]'); if (editGroup) { const group = state.groups.find((item) => item.id === editGroup.dataset.editGroup); if (group) openGroupEditor(group); } const copyGroup = event.target.closest('[data-copy-group-slug]'); if (copyGroup) { navigator.clipboard?.writeText(copyGroup.dataset.copyGroupSlug); copyGroup.textContent = 'Copied'; setTimeout(() => { copyGroup.textContent = 'Copy slug'; }, 1200); } const revoke = event.target.closest('[data-revoke-key]'); if (revoke && window.confirm('Revoke this client key?')) fetchJSON(`/api/client-keys/${encodeURIComponent(revoke.dataset.revokeKey)}`, { method: 'DELETE' }).then(loadKeys); const remove = event.target.closest('[data-remove-provider]'); if (remove && window.confirm('Remove this provider credential?')) fetchJSON(`/api/providers/credentials/${encodeURIComponent(remove.dataset.removeProvider)}`, { method: 'DELETE' }).then(() => Promise.all([loadProviders(), loadModels(), loadStatus()])); const copy = event.target.closest('[data-copy-target]'); if (copy) { const value = $(`#${copy.dataset.copyTarget}`)?.textContent || ''; navigator.clipboard?.writeText(value); copy.textContent = 'Copied'; setTimeout(() => { copy.textContent = 'Copy'; }, 1200); } const duplicateStage = event.target.closest('[data-duplicate-stage]'); if (duplicateStage) { const current = collectGroupDefinition(); const index = Number(duplicateStage.dataset.duplicateStage); const stage = current.stages[index]; if (stage) { current.stages.splice(index + 1, 0, { ...stage, sources: stage.sources.map((source) => ({ ...source })) }); renderGroupStages(current); previewCurrentGroup(); } return; } if (event.target.closest('.remove-stage')) { const cards = $$('#group-stage-list .group-stage-card'); if (cards.length > 1) { const current = collectGroupDefinition(); const index = Number(event.target.closest('.group-stage-card').dataset.stageIndex); current.stages.splice(index, 1); renderGroupStages(current); previewCurrentGroup(); } } if (event.target.classList.contains('modal-backdrop')) closeModal(event.target); if (event.target.closest('.close-modal')) closeModal(event.target.closest('.modal-backdrop')); });
  document.addEventListener('keydown', (event) => { if (event.key === 'Escape') { closeModelFilter(); $$('.modal-backdrop:not([hidden])').forEach(closeModal); } }); window.addEventListener('hashchange', () => navigate(window.location.hash.slice(1))); navigate(window.location.hash.slice(1) || 'overview');
  updateProviderFormMode(); setText('#base-url', `${window.location.origin}/v1`); Promise.all([loadStatus(), loadSummary(), loadRequests(), loadModelStats(), loadProviderStats(), loadGroupStats(), loadModels(), loadKeys(), loadProviders(), loadGroups(), loadNetworkSettings()]);
  const statsPanel = document.querySelector('[data-view-panel="stats"]'); if (statsPanel && !$('#subscription-stats-body')) { const article = document.createElement('article'); article.className = 'panel-card table-card stats-table-card'; article.innerHTML = '<div class="panel-heading stats-heading"><h3>Subscription economics</h3><span class="card-note">Dynamic blended pricing; observed 5h min/max</span></div><div class="table-wrap"><table><thead><tr><th>Provider</th><th>Tokens</th><th>Input / output per 1M</th><th>5h min / max</th></tr></thead><tbody id="subscription-stats-body"></tbody></table></div><div class="empty-state" id="subscription-stats-empty" hidden>No subscription usage yet.</div>'; statsPanel.append(article); }
  $('#update-settings-form')?.addEventListener('submit', async (event) => { event.preventDefault(); try { await fetchJSON('/api/updates/settings', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ enabled: $('#updates-enabled').checked, channel: $('#updates-channel').value, interval_seconds: Number($('#updates-interval').value) }) }); setText('#updates-feedback', 'Settings saved.'); await loadUpdates(); } catch (error) { setText('#updates-feedback', error.message); } });
  $('#updates-channel')?.addEventListener('change', () => { const note = $('#main-channel-note'); if (note) note.hidden = $('#updates-channel').value !== 'main'; });
  $('#updates-check')?.addEventListener('click', async () => { setText('#updates-feedback', 'Checking for updates…'); try { await fetchJSON('/api/updates/check', { method: 'POST' }); setTimeout(loadUpdates, 500); } catch (error) { setText('#updates-feedback', error.message); } });
  $('#updates-install')?.addEventListener('click', async () => { const payload = await fetchJSON('/api/updates'); if (!payload.available) return; setText('#updates-feedback', 'Downloading and restarting…'); await fetchJSON('/api/updates/install', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ version: payload.available.version }) }); });
  $('#refresh-button')?.addEventListener('click', loadUpdates);
  navigate = function(view) { const valid = ['overview', 'models', 'groups', 'stats', 'requests', 'access', 'settings']; state.view = valid.includes(view) ? view : 'overview'; $$('.view-panel').forEach((panel) => panel.classList.toggle('active', panel.dataset.viewPanel === state.view)); $$('.nav-item').forEach((item) => item.classList.toggle('active', item.dataset.view === state.view)); const meta = { overview: ['Overview', 'Your local routing desk at a glance.'], models: ['Models', 'Search provider routes and compare live pricing.'], groups: ['Groups', 'Callable aliases with ordered routing rules and fallbacks.'], stats: ['Statistics', 'Reliability, retries, response time, and cost by group, provider, and model.'], requests: ['Requests', 'Detailed usage, cost, and request outcomes.'], access: ['Access & keys', 'Manage the keys and providers behind your proxy.'], settings: ['Settings', 'Configure self-updating and inspect version history.'] }[state.view]; setText('#page-title', meta[0]); setText('#page-kicker', meta[0].toUpperCase()); setText('#page-description', meta[1]); $('#sidebar').classList.remove('open'); if (window.location.hash !== `#${state.view}`) history.replaceState(null, '', `#${state.view}`); };
  navigate(window.location.hash.slice(1) || 'overview');
  loadSubscriptionStats();
  loadUpdates();
  document.addEventListener('click', (event) => {
    const copyGroup = event.target.closest('[data-copy-group-slug]');
    if (!copyGroup) return;
    copyGroup.textContent = copyGroup.dataset.copiedLabel || '✓';
    setTimeout(() => { copyGroup.textContent = copyGroup.dataset.copyLabel || '⧉'; }, 1200);
  });
  $('#refresh-button').addEventListener('click', loadSubscriptionStats);
  $('#remote-access-save')?.addEventListener('click', async () => { const mode = $('#remote-access-mode').value; const hostname = $('#remote-access-hostname').value.trim().toLowerCase(); try { await fetchJSON('/api/remote-access', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ mode, hostname }) }); await loadRemoteAccess(); } catch (error) { setText('#remote-access-error', error.message); $('#remote-access-error').hidden = false; } });
  $('#remote-access-retry')?.addEventListener('click', async () => { try { await fetchJSON('/api/remote-access/retry', { method: 'POST' }); await loadRemoteAccess(); } catch (error) { setText('#remote-access-error', error.message); $('#remote-access-error').hidden = false; } });
  $('#remote-access-stop')?.addEventListener('click', async () => { if (!window.confirm('Stop sharing PayLessForAI remotely?')) return; try { await fetchJSON('/api/remote-access', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ mode: 'disabled', hostname: $('#remote-access-hostname').value }) }); await loadRemoteAccess(); } catch (error) { setText('#remote-access-error', error.message); $('#remote-access-error').hidden = false; } });
  $('#remote-access-forget')?.addEventListener('click', async () => { if (!window.confirm('Forget the saved Tailscale identity? You may need to remove the device from Tailscale separately.')) return; try { await fetchJSON('/api/remote-access/identity', { method: 'DELETE' }); await loadRemoteAccess(); } catch (error) { setText('#remote-access-error', error.message); $('#remote-access-error').hidden = false; } });
  const remoteAccessCard = document.querySelector('.remote-access-card');
  const settingsGrid = document.querySelector('[data-view-panel="settings"] .settings-grid');
  if (remoteAccessCard && settingsGrid) settingsGrid.prepend(remoteAccessCard);
  loadRemoteAccess();
})();
