(() => {
  const base = document.getElementById('base-url');
  const status = document.getElementById('status');
  base.textContent = `${window.location.origin}/v1`;
  fetch('/api/status').then((response) => response.json()).then((data) => {
    status.textContent = data.ready ? 'Ready · database migrated' : 'Starting · database is not ready';
  }).catch(() => { status.textContent = 'Unable to read status'; });

  const requests = document.getElementById('request-list');
  fetch('/api/requests?limit=20').then((response) => response.json()).then((payload) => {
    requests.replaceChildren();
    (payload.data || []).forEach((request) => {
      const item = document.createElement('li');
      const cost = request.actual_cost_pico_usd ?? request.estimated_cost_pico_usd ?? 0;
      item.textContent = `${request.model} · ${request.protocol} · ${request.state} · ${request.total_tokens || 0} tokens · $${(cost / 1e12).toFixed(8)}`;
      requests.append(item);
    });
    if (!payload.data || payload.data.length === 0) {
      const item = document.createElement('li');
      item.className = 'muted';
      item.textContent = 'No requests yet';
      requests.append(item);
    }
  }).catch(() => { requests.textContent = 'Unable to load request statistics'; });

  const list = document.getElementById('key-list');
  const form = document.getElementById('key-form');
  const secret = document.getElementById('new-key');
  const loadKeys = () => fetch('/api/client-keys').then((response) => response.json()).then((payload) => {
    list.replaceChildren();
    (payload.data || []).forEach((key) => {
      const item = document.createElement('li');
      item.textContent = `${key.label} · ${key.prefix}`;
      const revoke = document.createElement('button');
      revoke.textContent = 'Revoke';
      revoke.type = 'button';
      revoke.addEventListener('click', () => fetch(`/api/client-keys/${encodeURIComponent(key.id)}`, { method: 'DELETE' }).then(loadKeys));
      item.append(' ', revoke);
      list.append(item);
    });
    if (!payload.data || payload.data.length === 0) {
      const item = document.createElement('li');
      item.className = 'muted';
      item.textContent = 'No keys yet';
      list.append(item);
    }
  }).catch(() => { list.textContent = 'Unable to load keys'; });
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const label = document.getElementById('key-label').value;
    fetch('/api/client-keys', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ label }) })
      .then((response) => response.json()).then((payload) => {
        secret.hidden = false;
        secret.textContent = `Copy this key now; it will not be shown again: ${payload.secret}`;
        form.reset();
        return loadKeys();
      });
  });
  loadKeys();

  const providerList = document.getElementById('provider-list');
  const providerForm = document.getElementById('provider-form');
  const loadProviders = () => fetch('/api/providers/credentials').then((response) => response.json()).then((payload) => {
    providerList.replaceChildren();
    (payload.data || []).forEach((credential) => {
      const item = document.createElement('li');
      item.textContent = `${credential.provider} · ${credential.label}`;
      const remove = document.createElement('button');
      remove.textContent = 'Remove';
      remove.type = 'button';
      remove.addEventListener('click', () => fetch(`/api/providers/credentials/${encodeURIComponent(credential.id)}`, { method: 'DELETE' }).then(loadProviders));
      item.append(' ', remove);
      providerList.append(item);
    });
    if (!payload.data || payload.data.length === 0) {
      const item = document.createElement('li');
      item.className = 'muted';
      item.textContent = 'No provider credentials yet';
      providerList.append(item);
    }
  }).catch(() => { providerList.textContent = 'Unable to load provider credentials'; });
  providerForm.addEventListener('submit', (event) => {
    event.preventDefault();
    fetch('/api/providers/credentials', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ provider: document.getElementById('provider-name').value, label: document.getElementById('provider-label').value, api_key: document.getElementById('provider-key').value }) })
      .then((response) => response.json()).then(() => { providerForm.reset(); return loadProviders(); });
  });
  loadProviders();
})();
