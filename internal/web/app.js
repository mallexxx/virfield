'use strict';
let token = '', cursor = 0, pendingAcquire = null;
const releaseKeys = new Map();
const el = id => document.getElementById(id);
async function api(path, method = 'GET', body, key) {
  const res = await fetch('/api/v1/' + path, { method, headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json', ...(key ? {'Idempotency-Key': key} : {}) }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(8000) });
  const data = await res.json();
  if (!res.ok) { const error = new Error(data.error?.message || 'Request failed'); error.code = data.error?.code; throw error; }
  return data;
}
function text(tag, value) { const node = document.createElement(tag); node.textContent = value; return node; }
function report(err) { el('error').textContent = err.message; }
async function refresh() {
  const state = await api('status');
  el('capacity').textContent = `${state.capacity.used} / ${state.capacity.limit} slots occupied or reserved`;
  el('connection').textContent = state.observation.error ? 'Lume unavailable' : 'Connected';
  el('blockers').textContent = state.capacity.blockers.join(' · ');
  el('backend').textContent = state.observation.error?.message || 'Inventory checked: ' + new Date(state.observation.at).toLocaleTimeString();
  el('prepare').disabled = !!state.observation.error || state.capacity.available === 0;
  const selected = el('template').value;
  el('template').replaceChildren(...state.templates.map(t => {const option=text('option', t.id);option.value=t.id;return option;}));
  if (state.templates.some(t => t.id === selected)) el('template').value = selected;
  el('leases').replaceChildren(...state.leases.map(l => {
    const row = document.createElement('article');
    row.append(text('h3', l.vm_name), text('p', `${l.state} · expires ${new Date(l.expires_at).toLocaleString()}`), text('code', l.id));
    if (l.ip) row.append(text('p', `Guest IP: ${l.ip}`));
    if (l.error) row.append(text('p', l.error.message));
    const release = text('button', 'Release and delete VM');
    release.disabled = l.state === 'releasing' || l.state === 'quarantined';
    release.addEventListener('click', async () => {
      if (!confirm(`Stop and permanently delete ${l.vm_name}? Export results first.`)) return;
      release.disabled = true;
      if (!releaseKeys.has(l.id)) releaseKeys.set(l.id, crypto.randomUUID());
      try { await api('leases/' + encodeURIComponent(l.id) + '/release', 'POST', undefined, releaseKeys.get(l.id)); await refresh(); }
      catch (err) {report(err); release.disabled = false;}
    });
    row.append(release); return row;
  }));
  if (!state.leases.length) el('leases').append(text('p', 'No active leases.'));
  const page = await api('events?after=' + cursor);
  for (const event of page.events) el('events').prepend(text('li', `${new Date(event.at).toLocaleTimeString()} · ${event.type}: ${event.message}`));
  cursor = page.next_cursor;
  while (el('events').children.length > 30) el('events').lastChild.remove();
}
async function poll() { try { await refresh(); } catch(err) {report(err);el('connection').textContent='Connection lost';el('prepare').disabled=true;} finally {setTimeout(poll, 3000);} }
el('connect').addEventListener('submit', async event => {
  event.preventDefault(); token = el('token').value;
  try {await refresh();el('token').value='';el('login').hidden=true;el('console').hidden=false;el('error').textContent='';setTimeout(poll,3000);} catch(err) {report(err);}
});
el('acquire').addEventListener('submit', async event => {
  event.preventDefault(); el('prepare').disabled = true; el('error').textContent = '';
  // Preserve the exact payload and key on transport failure: a lost HTTP response
  // must not cause another lease when the user retries.
  pendingAcquire ||= {key:crypto.randomUUID(),body:{template:el('template').value,ttl_seconds:Number(el('ttl').value)}};
  try {await api('leases','POST',pendingAcquire.body,pendingAcquire.key);pendingAcquire=null;await refresh();}
  catch(err) {report(err); if (err.code && err.code !== 'internal_error') pendingAcquire=null;}
});
