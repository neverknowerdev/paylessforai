(() => {
  const state = { requests: [], models: [], modelStats: [], summary: {}, view: 'overview', detailDrawer: null, modelSort: { key: null, direction: null } };
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

  async function fetchJSON(url, options) {
    const response = await fetch(url, options);
    const payload = await response.json();
    if (!response.ok) throw new Error(payload?.error?.message || `Request failed (${response.status})`);
    return payload;
  }

  function setText(selector, value) { const element = $(selector); if (element) element.textContent = value; }
  function appendTextCell(row, value, className) { const cell = document.createElement('td'); if (className) cell.className = className; cell.textContent = value; row.append(cell); return cell; }
  function stateBadge(value) { const badge = document.createElement('span'); badge.className = `state-badge ${value || 'received'}`; badge.textContent = value || 'received'; return badge; }
  function providerBadge(provider) { const badge = document.createElement('span'); badge.className = 'badge provider'; badge.textContent = providerName(provider); return badge; }

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

  async function loadSummary() {
    try {
      state.summary = await fetchJSON('/api/stats/summary');
      const s = state.summary;
      const success = s.total_requests ? Math.round((s.succeeded_requests / s.total_requests) * 100) : 0;
      setText('#metric-total', s.total_requests ? `${formatNumber(s.total_requests)} · ${success}%` : '—');
      setText('#metric-tokens', formatNumber(s.total_tokens));
      setText('#metric-token-note', `${formatNumber(s.input_tokens)} in · ${formatNumber(s.output_tokens)} out · ${cacheHitPercent(s)} cache hit`);
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
    const s = state.summary || {}; const items = [['TOTAL', formatNumber(s.total_requests)], ['TOKENS', formatNumber(s.total_tokens)], ['ESTIMATED', formatUSD(s.estimated_cost_pico_usd)], ['ACTUAL', formatUSD(s.actual_cost_pico_usd)], ['SAVED', formatUSD(s.saved_cost_pico_usd)]];
    items.forEach(([label, value]) => { const item = document.createElement('div'); item.className = 'summary-item'; const title = document.createElement('span'); title.textContent = label; const number = document.createElement('strong'); number.textContent = value; item.append(title, number); container.append(item); });
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
    const items = [['REQUESTS · RELIABILITY', s.total_requests ? `${formatNumber(s.total_requests)} · ${success}%` : '—', `${formatNumber(s.total_attempts || 0)} attempts · ${formatNumber(retried)} retried`, 'accent-peach'], ['RESPONSE TIME', formatDuration(s.average_response_ms), `${formatDuration(s.fastest_response_ms)} fastest · ${formatDuration(s.slowest_response_ms)} slowest`, 'accent-blue'], ['TOKENS', formatNumber(s.total_tokens), `${formatNumber(s.input_tokens || 0)} in · ${formatNumber(s.output_tokens || 0)} out · ${cacheHitPercent(s)} cache hit`, 'accent-lime']];
    items.forEach(([label, value, note, accent]) => container.append(makeMetricCard(label, value, note, accent)));
    container.append(makeSpendMetricCard(s));
  }

  function renderModelStats() {
    const body = $('#model-stats-body'); const empty = $('#model-stats-empty'); if (!body) return; body.replaceChildren(); const items = [...state.modelStats].sort((a, b) => Number(b.free) - Number(a.free) || Number(b.requests || 0) - Number(a.requests || 0) || String(a.model).localeCompare(String(b.model)));
    items.forEach((item) => { const row = document.createElement('tr'); const model = appendTextCell(row, ''); const strong = document.createElement('strong'); strong.textContent = item.model; const small = document.createElement('small'); small.textContent = item.free ? 'Free route available' : 'Priced route'; model.append(strong, small); if (item.free) { const badge = document.createElement('span'); badge.className = 'badge free'; badge.textContent = 'FREE'; model.append(document.createTextNode(' '), badge); } appendTextCell(row, `${formatNumber(item.requests)} · ${formatNumber(item.total_attempts)} attempts`); const success = appendTextCell(row, ''); const successStrong = document.createElement('strong'); successStrong.textContent = `${Math.round(Number(item.success_rate_bps || 0) / 100)}%`; const successSmall = document.createElement('small'); successSmall.textContent = `${formatNumber(item.succeeded_requests || 0)} succeeded`; success.append(successStrong, successSmall); const retries = appendTextCell(row, ''); const retryStrong = document.createElement('strong'); retryStrong.textContent = formatNumber(item.retried_requests || 0); const retrySmall = document.createElement('small'); retrySmall.textContent = `${Math.round(Number(item.retry_rate_bps || 0) / 100)}% of requests`; retries.append(retryStrong, retrySmall); appendTextCell(row, `${formatDuration(item.fastest_response_ms)} · ${formatDuration(item.average_response_ms)} avg · ${formatDuration(item.slowest_response_ms)}`); const tokens = appendTextCell(row, ''); const tokensStrong = document.createElement('strong'); tokensStrong.textContent = formatCompact(item.total_tokens); const tokensSmall = document.createElement('small'); tokensSmall.textContent = `${formatCompact(item.input_tokens)} in · ${formatCompact(item.output_tokens)} out · ${formatCompact(item.cached_read_tokens)} cached · ${formatCompact(item.reasoning_tokens)} reasoning`; tokens.append(tokensStrong, tokensSmall); const cost = appendTextCell(row, ''); const costStrong = document.createElement('strong'); costStrong.textContent = formatUSD(item.actual_cost_pico_usd); const costSmall = document.createElement('small'); costSmall.textContent = `est ${formatUSD(item.estimated_cost_pico_usd)} · saved ${formatUSD(item.saved_cost_pico_usd)}`; cost.append(costStrong, costSmall); body.append(row); }); empty.hidden = items.length > 0;
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
      const row = document.createElement('tr'); row.className = 'data-row'; row.dataset.requestId = request.id;
      const id = appendTextCell(row, ''); const idStrong = document.createElement('strong'); idStrong.textContent = shortID(request.id); const idSmall = document.createElement('small'); idSmall.textContent = dateValue(request.received_at); id.append(idStrong, idSmall);
      const model = appendTextCell(row, ''); const modelStrong = document.createElement('strong'); modelStrong.textContent = request.model; const modelSmall = document.createElement('small'); modelSmall.textContent = protocolName(request.protocol); model.append(modelStrong, modelSmall);
      const provider = appendTextCell(row, ''); provider.append(providerBadge(request.provider || 'unknown'));
      appendTextCell(row, `${formatNumber(request.attempts)} attempt${request.attempts === 1 ? '' : 's'}`);
      appendTextCell(row, `${formatNumber(request.input_tokens)} in · ${formatNumber(request.output_tokens)} out`);
      const cost = appendTextCell(row, ''); const costStrong = document.createElement('strong'); costStrong.textContent = formatUSD(requestCost(request)); const costSmall = document.createElement('small'); costSmall.textContent = request.actual_cost_pico_usd != null ? 'actual' : 'estimated'; cost.append(costStrong, costSmall);
      const discount = appendTextCell(row, ''); const discountValue = discountAmount(request); const discountStrong = document.createElement('strong'); discountStrong.textContent = discountValue == null ? '—' : discountValue === 0 ? '$0' : formatUSD(discountValue); const discountSmall = document.createElement('small'); discountSmall.textContent = discountValue == null ? discountUnavailableLabel(request) : discountValue === 0 ? '' : `${discountPercent(request.discount_percent_bps)} saved`; discount.append(discountStrong, discountSmall);
      appendTextCell(row, formatDuration(request.duration_ms)); const status = document.createElement('td'); status.append(stateBadge(request.state)); row.append(status); appendTextCell(row, dateValue(request.completed_at)); body.append(row);
    });
    empty.hidden = filteredRequests().length > 0;
  }

  function showRequestDetail(id) {
    const request = state.requests.find((item) => item.id === id); const body = $('#requests-table-body'); const drawer = state.detailDrawer || $('#request-detail'); if (!request || !body || !drawer) return;
    body.querySelector('.request-detail-row')?.remove();
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
      const detail = document.createElement('small'); detail.textContent = attempt.error_class ? `${attempt.state} · ${attempt.error_class}: ${attempt.error_message || 'Provider error'}` : `${attempt.state} · ${dateValue(attempt.completed_at || attempt.started_at)}`;
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
    const selectedRow = body.querySelector(`tr[data-request-id="${CSS.escape(id)}"]`);
    if (!selectedRow) { drawer.hidden = true; return; }
    const detailRow = document.createElement('tr'); detailRow.className = 'request-detail-row'; const detailCell = document.createElement('td'); detailCell.colSpan = 10; detailCell.append(drawer); detailRow.append(detailCell); selectedRow.after(detailRow);
  }

  async function loadModels() {
    try { state.models = (await fetchJSON('/api/models')).data || []; renderModelTagOptions(); renderModels(); renderRouteSummary(); } catch (_) { setText('#models-table-body', 'Unable to load model catalog'); }
  }
  function renderModelTagOptions() { const select = $('#models-tag-filter'); if (!select) return; const selected = select.value; const tags = [...new Set(state.models.flatMap((model) => model.tags || []))].sort(); select.replaceChildren(new Option('All tags', 'all'), ...tags.map((tag) => new Option(tag, tag))); select.value = tags.includes(selected) ? selected : 'all'; }
  function filteredModels() {
    const search = ($('#models-search')?.value || '').trim().toLowerCase(); const provider = $('#models-provider-filter')?.value || 'all'; const tag = $('#models-tag-filter')?.value || 'all';
    const items = state.models.filter((model) => (provider === 'all' || model.provider === provider) && (tag === 'all' || (model.tags || []).includes(tag)) && (!search || `${model.model} ${model.upstream_model} ${model.provider} ${(model.tags || []).join(' ')} ${(model.input_modalities || []).join(' ')} ${(model.output_modalities || []).join(' ')}`.toLowerCase().includes(search)));
    const { key, direction } = state.modelSort; if (!key || !direction) return items;
    const price = (model, name) => { const value = Number(model.pricing?.[name]); return model.price_available && Number.isFinite(value) ? value : null; };
    const valueForSort = (model) => {
      if (key === 'model') return String(model.model || '').toLowerCase();
      if (key === 'provider') return providerName(model.provider).toLowerCase();
      if (key === 'modalities') return `${(model.input_modalities || []).join(' + ')} -> ${(model.output_modalities || []).join(' + ')}`.toLowerCase();
      if (key === 'tags') return (model.tags || []).join(' · ').toLowerCase();
      if (key === 'input') return price(model, 'input');
      if (key === 'discount') return model.discount_percent_bps == null ? null : Number(model.discount_percent_bps);
      if (key === 'context') return model.context_length == null ? null : Number(model.context_length);
      return null;
    };
    const compare = (a, b) => { const av = valueForSort(a); const bv = valueForSort(b); if (av == null && bv == null) return String(a.model).localeCompare(String(b.model)); if (av == null) return 1; if (bv == null) return -1; const result = typeof av === 'string' ? av.localeCompare(bv) : av - bv; return direction === 'desc' ? -result : result; };
    return items.sort(compare);
  }
  function updateModelSortIndicators() { $$('.table-sort').forEach((button) => { const active = state.modelSort.key === button.dataset.sortKey && state.modelSort.direction; const indicator = button.querySelector('.sort-indicator'); if (indicator) indicator.textContent = active ? (state.modelSort.direction === 'asc' ? '↑' : '↓') : '↕'; button.setAttribute('aria-sort', active ? (state.modelSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'); }); }
  function cycleModelSort(key) { if (state.modelSort.key !== key) state.modelSort = { key, direction: 'asc' }; else if (state.modelSort.direction === 'asc') state.modelSort.direction = 'desc'; else state.modelSort = { key: null, direction: null }; updateModelSortIndicators(); renderModels(); }
  function renderModels() {
    const body = $('#models-table-body'); const empty = $('#models-empty'); if (!body) return; const models = filteredModels(); body.replaceChildren(); setText('#catalog-count', `${formatNumber(models.length)} route${models.length === 1 ? '' : 's'}`); setText('#catalog-free-count', `${formatNumber(models.filter((model) => model.free).length)}`); setText('#models-saved', formatUSD(state.summary?.saved_cost_pico_usd)); setText('#models-saved-percent', state.summary?.saved_percent_bps == null ? 'No baseline' : `${discountPercent(state.summary.saved_percent_bps)} saved`);
    models.forEach((model) => { const row = document.createElement('tr'); const route = appendTextCell(row, ''); const strong = document.createElement('strong'); strong.textContent = model.model; const small = document.createElement('small'); small.textContent = model.upstream_model === model.model ? 'canonical upstream ID' : model.upstream_model; route.append(strong, small); if (model.free) { const badge = document.createElement('span'); badge.className = 'badge free'; badge.textContent = 'FREE'; route.append(document.createTextNode(' '), badge); } const provider = appendTextCell(row, ''); provider.append(providerBadge(model.provider)); const modalities = appendTextCell(row, ''); modalities.textContent = `${(model.input_modalities || []).join(' + ') || '—'} → ${(model.output_modalities || []).join(' + ') || '—'}`; modalities.className = 'modality-cell'; const tags = appendTextCell(row, (model.tags || []).join(' · ') || '—'); tags.className = 'tag-cell'; const pricing = appendTextCell(row, ''); pricing.className = 'pricing-cell'; appendModelPriceLine(pricing, model, 'input', 'in'); appendModelPriceLine(pricing, model, 'output', 'out'); pricing.title = 'Input / output per 1M tokens; crossed-out values are reference prices'; const discount = appendTextCell(row, ''); const discountStrong = document.createElement('strong'); discountStrong.textContent = modelDiscount(model.discount_percent_bps); const discountSmall = document.createElement('small'); discountSmall.textContent = modelDiscountDetail(model); discount.append(discountStrong, discountSmall); const context = appendTextCell(row, model.context_length ? formatCompact(model.context_length) : '—'); context.title = model.context_length ? `${formatNumber(model.context_length)} tokens` : ''; body.append(row); }); empty.hidden = models.length > 0; updateModelSortIndicators();
  }
  function renderRouteSummary() { const container = $('#route-summary'); if (!container) return; container.replaceChildren(); const counts = {}; state.models.forEach((model) => { counts[model.provider] = (counts[model.provider] || 0) + 1; }); Object.entries(counts).forEach(([provider, count]) => { const row = document.createElement('div'); row.className = 'route-row'; const dot = document.createElement('span'); dot.className = `provider-dot ${provider === 'surplus' ? 'surplus' : ''}`; row.append(dot); const main = document.createElement('span'); main.className = 'route-main'; const strong = document.createElement('strong'); strong.textContent = providerName(provider); const small = document.createElement('small'); small.textContent = `${count} discovered route${count === 1 ? '' : 's'}`; main.append(strong, small); row.append(main); const badge = document.createElement('span'); badge.className = 'badge provider'; badge.textContent = `${count}`; row.append(badge); container.append(row); }); if (!Object.keys(counts).length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'Add a provider credential to discover routes.'; container.append(empty); } }

  async function loadKeys() { try { const payload = await fetchJSON('/api/client-keys'); const list = $('#key-list'); list.replaceChildren(); (payload.data || []).forEach((key) => { const row = document.createElement('div'); row.className = 'credential-row'; const icon = document.createElement('span'); icon.className = 'credential-icon'; icon.textContent = '⌘'; row.append(icon); const main = document.createElement('span'); main.className = 'credential-main'; const strong = document.createElement('strong'); strong.textContent = key.label; const small = document.createElement('small'); small.textContent = `${key.prefix} · created ${dateValue(key.created_at)}`; main.append(strong, small); row.append(main); const revoke = document.createElement('button'); revoke.type = 'button'; revoke.dataset.revokeKey = key.id; revoke.textContent = key.revoked_at ? 'Revoked' : 'Revoke'; revoke.disabled = Boolean(key.revoked_at); row.append(revoke); list.append(row); }); if (!payload.data?.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'No client keys yet.'; list.append(empty); } } catch (_) { setText('#key-list', 'Unable to load client keys'); } }
  async function loadProviders() { try { const payload = await fetchJSON('/api/providers/credentials'); const list = $('#provider-list'); list.replaceChildren(); (payload.data || []).forEach((credential) => { const row = document.createElement('div'); row.className = 'credential-row'; const icon = document.createElement('span'); icon.className = `credential-icon ${credential.provider}`; icon.textContent = credential.provider === 'surplus' ? 'S' : 'O'; row.append(icon); const main = document.createElement('span'); main.className = 'credential-main'; const strong = document.createElement('strong'); strong.textContent = credential.label || providerName(credential.provider); const small = document.createElement('small'); small.textContent = `${providerName(credential.provider)} · ${credential.enabled ? 'Enabled' : 'Disabled'}`; main.append(strong, small); row.append(main); const remove = document.createElement('button'); remove.type = 'button'; remove.dataset.removeProvider = credential.id; remove.textContent = 'Remove'; row.append(remove); list.append(row); }); if (!payload.data?.length) { const empty = document.createElement('div'); empty.className = 'empty-state'; empty.textContent = 'No providers connected yet.'; list.append(empty); } } catch (_) { setText('#provider-list', 'Unable to load provider credentials'); } }

  function openModal(id) { const modal = $(`#${id}`); if (!modal) return; modal.hidden = false; document.body.classList.add('modal-open'); setTimeout(() => modal.querySelector('input, select')?.focus(), 0); }
  function closeModal(modal) { const element = typeof modal === 'string' ? $(`#${modal}`) : modal; if (element) element.hidden = true; if (!$$('.modal-backdrop:not([hidden])').length) document.body.classList.remove('modal-open'); }
  function navigate(view) { const valid = ['overview', 'models', 'stats', 'requests', 'access']; state.view = valid.includes(view) ? view : 'overview'; $$('.view-panel').forEach((panel) => panel.classList.toggle('active', panel.dataset.viewPanel === state.view)); $$('.nav-item').forEach((item) => item.classList.toggle('active', item.dataset.view === state.view)); const meta = { overview: ['Overview', 'Your local routing desk at a glance.'], models: ['Models', 'Search provider routes and compare live pricing.'], stats: ['Statistics', 'Reliability, retries, response time, and cost by model.'], requests: ['Requests', 'Detailed usage, cost, and request outcomes.'], access: ['Access & keys', 'Manage the keys and providers behind your proxy.'] }[state.view]; setText('#page-title', meta[0]); setText('#page-kicker', meta[0].toUpperCase()); setText('#page-description', meta[1]); $('#sidebar').classList.remove('open'); if (window.location.hash !== `#${state.view}`) history.replaceState(null, '', `#${state.view}`); }

  $('#key-form').addEventListener('submit', async (event) => { event.preventDefault(); try { const payload = await fetchJSON('/api/client-keys', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ label: $('#key-label').value }) }); const secret = $('#new-key'); secret.hidden = false; secret.textContent = `Copy this key now — it will not be shown again: ${payload.secret}`; $('#key-form').reset(); await loadKeys(); } catch (error) { setText('#new-key', error.message); $('#new-key').hidden = false; } });
  $('#provider-form').addEventListener('submit', async (event) => { event.preventDefault(); try { await fetchJSON('/api/providers/credentials', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ provider: $('#provider-name').value, label: $('#provider-label').value, api_key: $('#provider-key').value }) }); $('#provider-form').reset(); closeModal('provider-modal'); await Promise.all([loadProviders(), loadModels(), loadStatus()]); } catch (error) { window.alert(error.message); } });
  $('#open-key-modal').addEventListener('click', () => openModal('key-modal')); $('#open-provider-modal').addEventListener('click', () => openModal('provider-modal')); $('#menu-toggle').addEventListener('click', () => $('#sidebar').classList.toggle('open')); $('#refresh-button').addEventListener('click', () => Promise.all([loadStatus(), loadSummary(), loadRequests(), loadModelStats(), loadModels(), loadKeys(), loadProviders()])); $('#models-refresh').addEventListener('click', loadModels);
  $('#models-search').addEventListener('input', renderModels); $('#models-provider-filter').addEventListener('change', renderModels); $('#models-tag-filter').addEventListener('change', renderModels); $$('.table-sort').forEach((button) => button.addEventListener('click', () => cycleModelSort(button.dataset.sortKey))); $('#requests-search').addEventListener('input', renderRequestTable); $('#requests-state').addEventListener('change', renderRequestTable);
  document.addEventListener('click', (event) => { const target = event.target.closest('[data-view]'); if (target) { event.preventDefault(); navigate(target.dataset.view); } const requestTarget = event.target.closest('[data-request-id]'); if (requestTarget) showRequestDetail(requestTarget.dataset.requestId); const revoke = event.target.closest('[data-revoke-key]'); if (revoke && window.confirm('Revoke this client key?')) fetchJSON(`/api/client-keys/${encodeURIComponent(revoke.dataset.revokeKey)}`, { method: 'DELETE' }).then(loadKeys); const remove = event.target.closest('[data-remove-provider]'); if (remove && window.confirm('Remove this provider credential?')) fetchJSON(`/api/providers/credentials/${encodeURIComponent(remove.dataset.removeProvider)}`, { method: 'DELETE' }).then(() => Promise.all([loadProviders(), loadModels(), loadStatus()])); const copy = event.target.closest('[data-copy-target]'); if (copy) { const value = $(`#${copy.dataset.copyTarget}`)?.textContent || ''; navigator.clipboard?.writeText(value); copy.textContent = 'Copied'; setTimeout(() => { copy.textContent = 'Copy'; }, 1200); } if (event.target.classList.contains('modal-backdrop')) closeModal(event.target); if (event.target.closest('.close-modal')) closeModal(event.target.closest('.modal-backdrop')); });
  document.addEventListener('keydown', (event) => { if (event.key === 'Escape') $$('.modal-backdrop:not([hidden])').forEach(closeModal); }); window.addEventListener('hashchange', () => navigate(window.location.hash.slice(1))); navigate(window.location.hash.slice(1) || 'overview');
  setText('#base-url', `${window.location.origin}/v1`); Promise.all([loadStatus(), loadSummary(), loadRequests(), loadModelStats(), loadModels(), loadKeys(), loadProviders()]);
})();
