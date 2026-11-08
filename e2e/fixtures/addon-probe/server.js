'use strict';
// E2E fixture: a plain Node.js program that is "attached" to platform add-ons purely
// through environment variables, exactly like a customer app would be.
const http = require('http');
const os = require('os');

const PORT = parseInt(process.env.PORT || '8080', 10);
const started = new Date().toISOString();

async function probePostgres(url) {
  const { Client } = require('pg');
  const c = new Client({ connectionString: url, connectionTimeoutMillis: 5000 });
  await c.connect();
  try {
    await c.query('CREATE TABLE IF NOT EXISTS jp_probe (k text primary key, v text)');
    const v = 'pg-' + Date.now();
    await c.query('INSERT INTO jp_probe(k,v) VALUES($1,$2) ON CONFLICT (k) DO UPDATE SET v=EXCLUDED.v', ['probe', v]);
    const r = await c.query('SELECT v FROM jp_probe WHERE k=$1', ['probe']);
    return r.rows[0].v === v;
  } finally { await c.end(); }
}
async function probeRedis(url) {
  const Redis = require('ioredis');
  const r = new Redis(url, { connectTimeout: 5000, maxRetriesPerRequest: 1, lazyConnect: true });
  try {
    await r.connect();
    const v = 'redis-' + Date.now();
    await r.set('jp:probe', v, 'EX', 60);
    return (await r.get('jp:probe')) === v;
  } finally { r.disconnect(); }
}
async function probeMongo(url) {
  const { MongoClient } = require('mongodb');
  const c = new MongoClient(url, { serverSelectionTimeoutMS: 5000 });
  await c.connect();
  try {
    const col = c.db().collection('jp_probe');
    const v = 'mongo-' + Date.now();
    await col.updateOne({ _id: 'probe' }, { $set: { v } }, { upsert: true });
    return (await col.findOne({ _id: 'probe' })).v === v;
  } finally { await c.close(); }
}
async function probeMysql(url) {
  const mysql = require('mysql2/promise');
  const c = await mysql.createConnection({ uri: url, connectTimeout: 5000 });
  try {
    await c.query('CREATE TABLE IF NOT EXISTS jp_probe (k varchar(32) primary key, v varchar(64))');
    const v = 'mysql-' + Date.now();
    await c.query('REPLACE INTO jp_probe(k,v) VALUES(?,?)', ['probe', v]);
    const [rows] = await c.query('SELECT v FROM jp_probe WHERE k=?', ['probe']);
    return rows[0].v === v;
  } finally { await c.end(); }
}
async function probeRabbit(url) {
  const amqp = require('amqplib');
  const conn = await amqp.connect(url, { timeout: 5000 });
  try {
    const ch = await conn.createChannel();
    await ch.assertQueue('jp_probe', { durable: false, autoDelete: true });
    const msg = 'rabbit-' + Date.now();
    ch.sendToQueue('jp_probe', Buffer.from(msg));
    const got = await new Promise((res) => {
      const t = setTimeout(() => res(null), 4000);
      ch.consume('jp_probe', (m) => { clearTimeout(t); res(m && m.content.toString()); ch.ack(m); });
    });
    return got === msg;
  } finally { await conn.close(); }
}

const probes = {
  postgres: ['DATABASE_URL', probePostgres],
  redis: ['REDIS_URL', probeRedis],
  mongodb: ['MONGODB_URI', probeMongo],
  mysql: ['MYSQL_URL', probeMysql],
  rabbitmq: ['RABBITMQ_URL', probeRabbit],
};

const server = http.createServer(async (req, res) => {
  const u = new URL(req.url, 'http://x');
  const send = (code, obj) => { res.writeHead(code, { 'content-type': 'application/json' }); res.end(JSON.stringify(obj)); };
  if (u.pathname === '/' || u.pathname === '/health') {
    return send(200, { ok: true, app: 'jp-e2e-addon-probe', version: process.env.APP_VERSION || '1', started, host: os.hostname() });
  }
  if (u.pathname === '/env') {
    // reports which env NAMES are present (and values only for non-secret test markers)
    const names = Object.keys(process.env).sort();
    const markers = {};
    for (const k of names) if (k.startsWith('E2E_')) markers[k] = process.env[k];
    return send(200, { names, markers });
  }
  if (u.pathname === '/probe') {
    const only = u.searchParams.get('only');
    const out = {};
    for (const [name, [envName, fn]] of Object.entries(probes)) {
      if (only && only !== name) continue;
      const url = process.env[u.searchParams.get(name + '_env') || envName];
      if (!url) { out[name] = { configured: false }; continue; }
      try { out[name] = { configured: true, ok: await fn(url) }; }
      catch (e) { out[name] = { configured: true, ok: false, error: String(e && e.message || e).replace(/\/\/[^@\s]*@/g, '//***@') }; }
    }
    const failed = Object.values(out).some((v) => v.configured && !v.ok);
    return send(failed ? 500 : 200, out);
  }
  if (u.pathname === '/net') {
    // egress/isolation probe: tries TCP connects to given host:port list
    const net = require('net');
    const targets = (u.searchParams.get('t') || '').split(',').filter(Boolean);
    const results = {};
    await Promise.all(targets.map((t) => new Promise((resolve) => {
      const [h, p] = t.split(':');
      const s = net.connect({ host: h, port: parseInt(p, 10), timeout: 2500 });
      s.on('connect', () => { results[t] = 'open'; s.destroy(); resolve(); });
      s.on('timeout', () => { results[t] = 'timeout'; s.destroy(); resolve(); });
      s.on('error', (e) => { results[t] = e.code || 'error'; resolve(); });
    })));
    return send(200, results);
  }
  if (u.pathname === '/mem') { return send(200, process.memoryUsage()); }
  send(404, { error: 'not found' });
});
server.listen(PORT, '0.0.0.0', () => console.log('listening on ' + PORT));
process.on('SIGTERM', () => server.close(() => process.exit(0)));
