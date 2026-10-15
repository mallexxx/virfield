'use strict';

let token = '';
const tokenStorageKey = 'virfield.apiToken';
let cursor = 0;
let pendingAcquire = null;
let refreshInFlight = null;
let pollTimer = null;
let admissionState = null;
const releaseKeys = new Map();
const imageKeys = new Map();
const tunnelAddresses = new Map();
const el = id => document.getElementById(id);

const invalidTokenMessage = 'Invalid token format: use the plain-text API token (printable ASCII, no spaces).';

async function api(path, method = 'GET', body, key) {
  const headers = {Authorization: 'Bearer ' + token};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (key) headers['Idempotency-Key'] = key;
  const payload = body === undefined ? undefined : JSON.stringify(body);
  let request;
  // The browser rejects header values with non-Latin-1 or control characters
  // before anything is sent, so this is a bad token, not an unreachable API.
  try { request = new Request('/api/v1/' + path, {method, headers, body: payload}); }
  catch (_) {
    const error = new Error(invalidTokenMessage);
    error.code = 'invalid_token';
    throw error;
  }
  let response;
  let raw;
  try {
    response = await fetch(request);
    raw = await response.text();
  } catch (_) {
    throw new Error('Cannot reach Virfield API at ' + window.location.origin);
  }
  let data = {};
  try { data = raw ? JSON.parse(raw) : {}; }
  catch (_) { throw new Error(`Virfield returned invalid data (HTTP ${response.status})`); }
  if (!response.ok) {
    const detail = data && data.error;
    const error = new Error((detail && detail.message) || `Request failed (HTTP ${response.status})`);
    error.code = detail && detail.code;
    throw error;
  }
  return data;
}

function node(tag, value, className) {
  const result = document.createElement(tag);
  if (value !== undefined) result.textContent = value;
  if (className) result.className = className;
  return result;
}

function setConnection(label, state) {
  const target = el('connection');
  target.className = 'connection ' + state;
  target.replaceChildren(node('i'), document.createTextNode(' ' + label));
}

function report(error) {
  const target = el('error');
  target.textContent = error.message;
  target.hidden = false;
  clearTimeout(target.hideTimer);
  target.hideTimer = setTimeout(() => { target.hidden = true; }, 8000);
}

function tokenValidationError(value) {
  const candidate = value.trim();
  if (!candidate) return 'Enter the API token.';
  // Invalid header characters make fetch throw before Virfield receives a request.
  if (!/^[\x21-\x7E]+$/.test(candidate)) {
    return invalidTokenMessage;
  }
  return '';
}

async function connectWithToken(candidate) {
  token = candidate;
  const button = el('connect-button');
  button.disabled = true;
  button.textContent = 'Connecting…';
  el('login-error').hidden = true;
  try {
    await refresh();
    let storageFailed = false;
    try { localStorage.setItem(tokenStorageKey, token); }
    catch (_) { storageFailed = true; }
    el('token').value = '';
    el('login').hidden = true;
    el('console').hidden = false;
    el('nav').hidden = false;
    el('refresh').hidden = false;
    el('disconnect').hidden = false;
    clearTimeout(pollTimer);
    pollTimer = setTimeout(poll, 3000);
    if (storageFailed) report(new Error('Connected, but the browser could not save the token. Check its storage/privacy settings.'));
  } catch (error) {
    if (error.code === 'unauthorized' || error.code === 'invalid_token') {
      try { localStorage.removeItem(tokenStorageKey); } catch (_) {}
      token = '';
      el('token').value = '';
    }
    el('login-error').textContent = error.message;
    el('login-error').hidden = false;
    report(error);
  } finally {
    button.disabled = false;
    button.textContent = 'Connect';
  }
}

function gib(bytes, digits = 1) {
  return (Number(bytes || 0) / 2 ** 30).toFixed(digits) + ' GiB';
}

function disk(bytes) {
  const value = Number(bytes || 0);
  return value >= 2 ** 40 ? (value / 2 ** 40).toFixed(1) + ' TiB' : gib(value, 0);
}

function shortTime(value) {
  if (!value || String(value).startsWith('0001-')) return '—';
  return new Date(value).toLocaleString([], {month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit'});
}

function reason(value) {
  return value ? value.replace(/_/g, ' ') : '';
}

function status(value, why) {
  const wrap = node('span', undefined, 'status ' + (value || 'unknown'));
  wrap.append(document.createTextNode(value || 'unknown'));
  if (why) wrap.title = reason(why);
  return wrap;
}

function meter(used, total, label) {
  const wrap = node('div');
  const labels = node('div', undefined, 'meter-label');
  labels.append(node('span', label), node('span', total > 0 ? `${gib(used, 1)} / ${gib(total, 1)}` : '—'));
  const bar = node('div', undefined, 'meter');
  const fill = node('progress');
  fill.max = Math.max(1, Number(total || 0));
  fill.value = Math.max(0, Math.min(Number(used || 0), fill.max));
  bar.append(fill);
  wrap.append(labels, bar);
  return wrap;
}

function table(headers, rows) {
  if (!rows.length) return node('div', 'No records.', 'empty');
  const result = node('table');
  const head = node('thead');
  const headRow = node('tr');
  for (const label of headers) headRow.append(node('th', label));
  head.append(headRow);
  const body = node('tbody');
  for (const cells of rows) {
    const row = node('tr');
    for (const cell of cells) {
      const td = node('td');
      if (cell instanceof Node) td.append(cell); else td.textContent = cell ?? '—';
      row.append(td);
    }
    body.append(row);
  }
  result.append(head, body);
  return result;
}

function renderSummary(state) {
  const resources = state.resources;
  el('runtime').textContent = state.observation.provider || 'legacy';
  el('capacity').textContent = `${state.capacity.used} used · ${state.capacity.available} free · ${state.capacity.limit} total`;
  el('cpu').textContent = resources ? `${resources.reserved.cpu} / ${resources.limits.cpu} reserved` : 'reported per host';
  el('memory').textContent = resources ? `${gib(resources.reserved.memory_bytes, 0)} / ${gib(resources.limits.memory_bytes, 0)}` : 'reported per host';
  el('backend').textContent = state.observation.error?.message || shortTime(state.observation.at);
  const blockers = [...(state.capacity.blockers || [])];
  if (resources?.error) blockers.push(resources.error);
  el('blockers').textContent = blockers.join(' · ');
  el('blockers').hidden = blockers.length === 0;
  setConnection(state.observation.error ? 'runtime unavailable' : 'connected', state.observation.error ? 'offline' : 'online');
}

function renderDevices(state) {
  const hosts = state.observation.hosts || [];
  const containers = state.observation.storage_containers || [];
  const vms = state.observation.vms || [];
  const root = el('devices');
  root.replaceChildren();
  el('fleet-count').textContent = `${hosts.length} hosts · ${containers.length} storage · ${vms.length} VMs`;

  for (const host of hosts) {
    const group = node('div', undefined, 'host');
    const row = node('div', undefined, 'host-row');
    const identity = node('div');
    identity.append(node('div', host.name || host.id, 'name'), node('div', `${host.os || 'unknown'} ${host.arch || ''} · ${host.provider}`, 'subtle'));
    const capacity = host.capacity || {};
    const slots = node('div');
    slots.append(node('div', `${capacity.used_vms || 0} / ${capacity.max_vms || 0} VMs`, 'name'), node('div', `${gib(capacity.memory_reserve_bytes, 0)} host reserve`, 'subtle'));
    const ram = meter(capacity.allocated?.memory_bytes, capacity.allocatable?.memory_bytes, 'RAM');
    const actions = node('div', undefined, 'row-actions');
    if (host.wake_on_lan && host.availability !== 'available') {
      const wake = node('button', 'Wake');
      wake.addEventListener('click', async () => {
        try { wake.disabled = true; await api('hosts/' + encodeURIComponent(host.id) + '/wake', 'POST'); }
        catch (error) { report(error); wake.disabled = false; }
      });
      actions.append(wake);
    }
    row.append(identity, status(host.availability, host.unavailable_reason), slots, ram, actions);
    group.append(row);

    const hostContainers = containers.filter(item => item.host_id === host.id);
    for (const container of hostContainers) {
      const storageRow = node('div', undefined, 'storage-row');
      const title = node('div');
      title.append(node('div', '▰ ' + container.id, 'name'), node('div', container.path || `${container.kind} storage`, 'subtle'));
      const available = Number(container.available_bytes || 0);
      const total = Number(container.capacity_bytes || 0);
      const usage = total > 0 ? meter(total - available, total, 'Disk') : node('span', 'capacity unavailable', 'subtle');
      const contents = vms.filter(vm => vm.container_id === container.id);
      storageRow.append(title, status(container.availability, container.unavailable_reason), usage, node('span', container.device_identity || '—', 'subtle'), node('span', `${contents.length} VMs`, 'subtle'));
      group.append(storageRow);
      for (const vm of contents) group.append(vmRow(vm));
    }
    for (const vm of vms.filter(item => item.host_id === host.id && !hostContainers.some(c => c.id === item.container_id))) group.append(vmRow(vm));
    root.append(group);
  }
  if (!hosts.length) root.append(node('div', 'No hosts reported by this runtime.', 'empty'));
}

function vmRow(vm) {
  const row = node('div', undefined, 'vm-row');
  const title = node('div');
  title.append(node('div', '↳ ' + vm.name, 'name'), node('div', `${vm.os || 'unknown'} · ${vm.provider || 'runtime'}`, 'subtle'));
  const resources = `${vm.resources?.cpu || 0} CPU · ${gib(vm.resources?.memory_bytes, 0)}`;
  row.append(title, status(vm.state, vm.unavailable_reason), node('span', resources, 'subtle'), node('span', vm.ip || '—', 'subtle'), node('span', vm.ssh_available ? 'SSH ready' : '—', 'subtle'));
  return row;
}

function renderLeaseForm(state) {
  admissionState = state;
  const currentTemplate = el('template').value;
  const availability = new Map((state.template_availability || []).map(item => [item.template_id, item]));
  el('template').replaceChildren(...state.templates.map(template => {
    const current = availability.get(template.id);
    const option = node('option', current?.availability === 'unavailable' ? `${template.id} · ${reason(current.unavailable_reason)}` : template.id);
    option.value = template.id;
    option.disabled = current?.availability === 'unavailable';
    return option;
  }));
  if (state.templates.some(template => template.id === currentTemplate)) el('template').value = currentTemplate;
  renderDestinations(state);
  el('prepare').disabled = Boolean(state.observation.error) || state.capacity.available === 0;
  renderAdmission();
}

function renderDestinations(state) {
  const currentDestination = el('destination').value;
  const estimates = state.resource_admission?.[el('template').value]?.destinations;
  const locations = estimates ? Object.keys(estimates).sort() : [...new Set(state.templates.map(template => template.location).filter(Boolean))].sort();
  const automatic = node('option', 'Automatic');
  automatic.value = '';
  el('destination').replaceChildren(automatic, ...locations.map(location => {
    const option = node('option', location);
    option.value = location;
    return option;
  }));
  if (locations.includes(currentDestination)) el('destination').value = currentDestination;
}

function renderAdmission() {
  const report = admissionState?.resource_admission?.[el('template').value];
  const snapshot = el('destination').value ? report?.destinations?.[el('destination').value] : report?.acquire;
  const text = !snapshot ? 'Resource estimate unavailable for this selection.' :
    `${report.inventory_verified ? '' : 'Inventory is unverified; admission is paused.\n'}${snapshot.allowed ? 'Fits resource quotas' : 'Resource refusal'} on ${snapshot.host}${snapshot.selected_location ? ' / ' + snapshot.selected_location : ''}.\n${snapshot.summary.replaceAll('; ', '\n')}\nEstimate only: current reservations and inventory are checked again when preparing the VM.`;
  el('resource-admission-text').textContent = text;
}

el('template').addEventListener('change', () => {
  if (admissionState) renderDestinations(admissionState);
  renderAdmission();
});
el('destination').addEventListener('change', renderAdmission);

function renderLeases(state) {
  const leases = state.leases.filter(lease => lease.purpose !== 'image');
  el('lease-count').textContent = String(leases.length);
  const rows = leases.map(lease => {
    const identity = node('div');
    identity.append(node('div', lease.vm_name, 'name'), node('code', lease.id));
    const connection = node('div');
    connection.append(node('div', lease.ip || 'IP pending'), node('div', tunnelAddresses.get(lease.id) || '', 'subtle'));
    const actions = node('div', undefined, 'row-actions');
    if (lease.state === 'ready' && lease.ssh) {
      const tunnel = node('button', 'SSH tunnel');
      tunnel.addEventListener('click', async () => {
        try {
          tunnel.disabled = true;
          const result = await api('leases/' + encodeURIComponent(lease.id) + '/tunnel', 'POST');
          tunnelAddresses.set(lease.id, result.address);
          await refresh();
        } catch (error) { report(error); tunnel.disabled = false; }
      });
      actions.append(tunnel);
    }
    if (lease.state === 'ready' && lease.ssh && lease.image_id) {
      const desktop = node('button', 'Desktop');
      desktop.addEventListener('click', async () => {
        try {
          desktop.disabled = true;
          const result = await api('leases/' + encodeURIComponent(lease.id) + '/tunnel', 'POST', {service: 'desktop'});
          window.location.href = result.url;
        } catch (error) { report(error); }
        desktop.disabled = false;
      });
      actions.append(desktop);
    }
    const release = node('button', 'Release', 'danger');
    release.disabled = ['releasing', 'quarantined'].includes(lease.state);
    release.addEventListener('click', async () => {
      if (!confirm(`Stop and permanently delete ${lease.vm_name}?`)) return;
      release.disabled = true;
      if (!releaseKeys.has(lease.id)) releaseKeys.set(lease.id, crypto.randomUUID());
      try { await api('leases/' + encodeURIComponent(lease.id) + '/release', 'POST', undefined, releaseKeys.get(lease.id)); await refresh(); }
      catch (error) { report(error); release.disabled = false; }
    });
    actions.append(release);
    return [identity, status(lease.state), lease.template, lease.location, connection, shortTime(lease.expires_at), actions];
  });
  el('leases').replaceChildren(table(['VM / lease', 'State', 'Image', 'Storage', 'Connection', 'Expires', ''], rows));
}

function renderImages(state) {
  const availability = new Map((state.template_availability || []).map(item => [item.template_id, item]));
  const imageBusy = state.leases.some(lease => lease.purpose === 'image' && lease.state !== 'image_ready');
  const rows = state.templates.map(template => {
    const vm = (state.observation.vms || []).find(item => item.name === template.name && item.location === template.location);
    const current = availability.get(template.id) || {availability: 'unknown'};
    const identity = node('div');
    identity.append(node('div', template.id, 'name'), node('div', template.source || template.name, 'subtle'));
    const details = template.image ? `${template.image.build || 'any build'} · SIP ${template.image.disable_sip ? 'off' : 'on'}${template.image.provision ? ' · ' + template.image.provision : ''}` : `${template.os || 'macOS'} · ${template.arch || 'native'}`;
    const actions = node('div', undefined, 'row-actions');
    if (template.image) {
      const build = node('button', 'Build');
      build.disabled = Boolean(vm) || Boolean(state.observation.error) || state.capacity.available === 0 || state.leases.some(lease => lease.purpose === 'image' && lease.template === template.id && lease.state !== 'image_ready');
      build.addEventListener('click', async () => {
        if (!confirm(`Build ${template.id}?`)) return;
        build.disabled = true;
        const action = 'build:' + template.id;
        if (!imageKeys.has(action)) imageKeys.set(action, crypto.randomUUID());
        try { await api('images/' + encodeURIComponent(template.id) + '/build', 'POST', {}, imageKeys.get(action)); imageKeys.delete(action); await refresh(); }
        catch (error) { report(error); if (error.code && error.code !== 'internal_error') imageKeys.delete(action); }
      });
      actions.append(build);
    }
    const remove = node('button', 'Delete', 'danger');
    remove.disabled = !vm || vm.state !== 'stopped' || imageBusy || Boolean(state.observation.error);
    remove.addEventListener('click', async () => {
      if (prompt(`Type ${template.name} to delete it permanently:`) !== template.name) return;
      remove.disabled = true;
      const action = 'delete:' + template.id;
      if (!imageKeys.has(action)) imageKeys.set(action, crypto.randomUUID());
      try { await api('images/' + encodeURIComponent(template.id) + '/delete', 'POST', {confirm_name: template.name}, imageKeys.get(action)); imageKeys.delete(action); await refresh(); }
      catch (error) { report(error); if (error.code && error.code !== 'internal_error') imageKeys.delete(action); }
    });
    actions.append(remove);
    return [identity, status(current.availability, current.unavailable_reason), template.provider || state.observation.provider, template.location, details, vm?.state || 'not local', actions];
  });
  el('images').replaceChildren(table(['Image', 'Availability', 'Runtime', 'Storage', 'Policy', 'Artifact', ''], rows));
}

function renderJobs(state) {
  const rows = state.jobs.map(job => [node('code', job.id), job.kind, status(job.state), job.progress || job.phase || '—', shortTime(job.updated_at)]);
  el('jobs').replaceChildren(table(['Job', 'Kind', 'State', 'Progress', 'Updated'], rows));
}

async function renderEvents() {
  const page = await api(cursor === 0 ? 'events?tail=true&limit=40' : 'events?after=' + cursor);
  for (const event of page.events) {
    const row = node('li');
    row.append(node('time', new Date(event.at).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit', second: '2-digit'})), node('b', event.type), node('span', event.message));
    el('events').prepend(row);
  }
  cursor = page.next_cursor;
  while (el('events').children.length > 60) el('events').lastChild.remove();
  if (!el('events').children.length) el('events').append(node('li', 'No events.'));
}

function refresh() {
  return refreshInFlight ||= refreshState().finally(() => { refreshInFlight = null; });
}

async function refreshState() {
  const state = await api('status');
  for (const id of tunnelAddresses.keys()) {
    if (!state.leases.some(lease => lease.id === id && lease.state === 'ready')) tunnelAddresses.delete(id);
  }
  renderSummary(state);
  renderDevices(state);
  renderLeaseForm(state);
  renderLeases(state);
  renderImages(state);
  renderJobs(state);
  await renderEvents();
}

async function poll() {
  try { await refresh(); }
  catch (error) { setConnection('connection lost', 'offline'); report(error); el('prepare').disabled = true; }
  finally { pollTimer = setTimeout(poll, 3000); }
}

el('connect').addEventListener('submit', async event => {
  event.preventDefault();
  const input = el('token').value;
  const validationError = tokenValidationError(input);
  if (validationError) {
    token = '';
    el('login-error').textContent = validationError;
    el('login-error').hidden = false;
    return;
  }
  await connectWithToken(input.trim());
});

el('disconnect').addEventListener('click', () => {
  clearTimeout(pollTimer);
  try { localStorage.removeItem(tokenStorageKey); } catch (_) {}
  token = '';
  el('console').hidden = true;
  el('nav').hidden = true;
  el('refresh').hidden = true;
  el('disconnect').hidden = true;
  el('login').hidden = false;
  el('login-error').hidden = true;
  el('error').hidden = true;
  setConnection('disconnected', '');
});

try {
  const savedToken = localStorage.getItem(tokenStorageKey);
  if (savedToken) {
    const validationError = tokenValidationError(savedToken);
    if (validationError) localStorage.removeItem(tokenStorageKey);
    else {
      el('token').value = savedToken;
      void connectWithToken(savedToken);
    }
  }
} catch (_) { /* Storage may be disabled; manual sign-in remains available. */ }

el('refresh').addEventListener('click', async () => {
  try { el('refresh').disabled = true; await refresh(); }
  catch (error) { report(error); }
  finally { el('refresh').disabled = false; }
});

for (const button of document.querySelectorAll('.nav-item')) {
  button.addEventListener('click', () => {
    for (const item of document.querySelectorAll('.nav-item')) item.classList.toggle('active', item === button);
    for (const view of document.querySelectorAll('.view')) view.classList.toggle('active', view.id === 'view-' + button.dataset.view);
  });
}

el('new-lease').addEventListener('click', () => { el('acquire').hidden = false; el('new-lease').hidden = true; });
el('cancel-lease').addEventListener('click', () => { el('acquire').hidden = true; el('new-lease').hidden = false; });

el('acquire').addEventListener('submit', async event => {
  event.preventDefault();
  const body = {
    template: el('template').value,
    destination_location: el('destination').value,
    ttl_seconds: Number(el('ttl').value),
    ssh_public_key: el('ssh-key').value,
  };
  if (pendingAcquire && JSON.stringify(pendingAcquire.body) !== JSON.stringify(body)) {
    report(new Error('The previous request may have succeeded. Keep the same values for a safe retry or inspect Activity.'));
    return;
  }
  pendingAcquire ||= {key: crypto.randomUUID(), body};
  el('prepare').disabled = true;
  try {
    await api('leases', 'POST', pendingAcquire.body, pendingAcquire.key);
    pendingAcquire = null;
    el('ssh-key').value = '';
    el('acquire').hidden = true;
    el('new-lease').hidden = false;
    await refresh();
  } catch (error) {
    report(error);
    if (error.code && error.code !== 'internal_error') pendingAcquire = null;
    el('prepare').disabled = false;
  }
});
