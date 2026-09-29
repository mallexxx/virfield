'use strict';
let token = '', cursor = 0, pendingAcquire = null;
const releaseKeys = new Map();
const imageKeys = new Map();
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
  el('resources').textContent = state.resources ? `CPU ${state.resources.reserved.cpu}/${state.resources.limits.cpu} · RAM ${(state.resources.reserved.memory_bytes / 2**30).toFixed(0)}/${(state.resources.limits.memory_bytes / 2**30).toFixed(0)} GiB reserved` + (state.resources.error ? ' · ' + state.resources.error : '') : '';
  el('backend').textContent = state.observation.error?.message || 'Inventory checked: ' + new Date(state.observation.at).toLocaleTimeString();
  const imageBusy = state.leases.some(l => l.purpose === 'image' && l.state !== 'image_ready');
  el('prepare').disabled = !!state.observation.error || state.capacity.available === 0 || imageBusy;
  const selected = el('template').value;
  el('template').replaceChildren(...state.templates.map(t => {const option=text('option', t.id);option.value=t.id;return option;}));
  if (state.templates.some(t => t.id === selected)) el('template').value = selected;
  el('leases').replaceChildren(...state.leases.filter(l => l.purpose !== 'image').map(l => {
    const row = document.createElement('article');
    row.append(text('h3', l.vm_name), text('p', `${l.state} · expires ${new Date(l.expires_at).toLocaleString()}`), text('code', l.id));
    if (l.ip) row.append(text('p', `Guest IP: ${l.ip}`));
    if (l.ssh) row.append(text('p', 'Client key: ' + l.ssh.client_key_fingerprint), text('code', 'Host key: ' + l.ssh.host_key));
    if (l.error) row.append(text('p', l.error.message));
    if (l.state === 'ready' && l.ssh) {const open = text('button', 'Open SSH tunnel'); const endpoint = text('code', ''); open.addEventListener('click', async () => {try {const t = await api('leases/' + encodeURIComponent(l.id) + '/tunnel', 'POST'); endpoint.textContent = t.address + ' · use your lease SSH key and pinned host key';} catch(err) {report(err);}});row.append(open, endpoint);}
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
  if (!state.leases.some(l => l.purpose !== 'image')) el('leases').append(text('p', 'No active leases.'));
  el('jobs').replaceChildren(...state.jobs.map(j => {
    const row = document.createElement('article');
    row.append(text('h3', `${j.kind} · ${j.state}`), text('p', j.progress || j.phase), text('code', j.id));
    if (j.error) row.append(text('p', j.error.message));
    return row;
  }));
  if (!state.jobs.length) el('jobs').append(text('p', 'No active jobs.'));
  el('images').replaceChildren(...state.templates.map(t => {
    const row = document.createElement('article');
    const vm = state.observation.vms.find(v => v.name === t.name && v.location === t.location);
    row.append(text('h3', t.id), text('p', `${t.name} · ${vm?.state || 'absent'}`));
    if (t.image) {
      row.append(text('p', `Build ${t.image.build} · guest SIP ${t.image.disable_sip ? 'disabled' : 'enabled'}`), text('p', t.image.provision ? 'Tools recipe: ' + t.image.provision : 'Base OS image · development tools not provisioned'));
      const build = text('button', 'Build image');
      build.disabled = !!vm || !!state.observation.error || state.capacity.used > 0 || imageBusy;
      build.addEventListener('click', async () => {
        if (!confirm(`Download and install ${t.name}? This applies the displayed guest SIP policy and reserves the manager during preparation.`)) return;
        build.disabled = true;
        const action = 'build:' + t.id;
        if (!imageKeys.has(action)) imageKeys.set(action, crypto.randomUUID());
        try {await api('images/' + encodeURIComponent(t.id) + '/build', 'POST', {}, imageKeys.get(action));imageKeys.delete(action);await refresh();}
        catch (err) {report(err);if (err.code && err.code !== 'internal_error') imageKeys.delete(action);}
      });
      row.append(build);
    }
    const remove = text('button', 'Delete image');
    remove.disabled = !vm || vm.state !== 'stopped' || imageBusy || !!state.observation.error;
    remove.addEventListener('click', async () => {
      if (prompt(`Permanently delete image? Type its exact name: ${t.name}`) !== t.name) return;
      remove.disabled = true;
      const action = 'delete:' + t.id;
      if (!imageKeys.has(action)) imageKeys.set(action, crypto.randomUUID());
      try {await api('images/' + encodeURIComponent(t.id) + '/delete','POST',{confirm_name:t.name},imageKeys.get(action));imageKeys.delete(action);await refresh();}
      catch (err) {report(err);if (err.code && err.code !== 'internal_error') imageKeys.delete(action);}
    });
    row.append(remove);
    return row;
  }));
  const page = await api(cursor === 0 ? 'events?tail=true&limit=30' : 'events?after=' + cursor);
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
  pendingAcquire ||= {key:crypto.randomUUID(),body:{template:el('template').value,ttl_seconds:Number(el('ttl').value),ssh_public_key:el('ssh-key').value}};
  try {await api('leases','POST',pendingAcquire.body,pendingAcquire.key);pendingAcquire=null;el('ssh-key').value='';await refresh();}
  catch(err) {report(err); if (err.code && err.code !== 'internal_error') pendingAcquire=null;}
});
