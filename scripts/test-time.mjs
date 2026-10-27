import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const cases = {
  UTC: [[2025, 1, 15, 12, 34, 56], [2025, 7, 15, 12, 34, 56]],
  'Asia/Shanghai': [[2025, 1, 15, 20, 34, 56], [2025, 7, 15, 20, 34, 56]],
  'America/New_York': [[2025, 1, 15, 7, 34, 56], [2025, 7, 15, 8, 34, 56]],
};

if (process.argv[2] !== '--child') {
  for (const TZ of Object.keys(cases)) {
    const result = spawnSync(process.execPath, [fileURLToPath(import.meta.url), '--child'], {
      env: { ...process.env, TZ }, stdio: 'inherit',
    });
    assert.equal(result.status, 0, `${TZ}: ${result.error || 'test failed'}`);
  }
} else {
  const source = await readFile(new URL('../public/js/lib/dom.js', import.meta.url), 'utf8');
  const { dateLabel, browserTimeZoneLabel } = await import(
    'data:text/javascript;base64,' + Buffer.from(source).toString('base64')
  );
  const parts = (value) => dateLabel(value).match(/\d+/g).map(Number);
  const dates = ['2025-01-15T12:34:56Z', '2025-07-15T12:34:56Z'];
  dates.forEach((date, i) => assert.deepEqual(parts(date), cases[process.env.TZ][i]));
  assert.equal(dateLabel(dates[0]), dateLabel('2025-01-15T20:34:56+08:00'));
  assert.equal(dateLabel(dates[1]), dateLabel('2025-07-15T08:34:56-04:00'));
  assert.deepEqual(parts('2025-01-15T00:00:00'), [2025, 1, 15, 0, 0, 0]);
  for (const value of ['', null, undefined, 'invalid', '2025-99-99T00:00:00Z']) {
    assert.equal(dateLabel(value), '暂无记录');
  }
  assert.equal(browserTimeZoneLabel(), `浏览器本地时区（${process.env.TZ}）`);
  console.log(`PASS ${process.env.TZ}: winter/summer, offsets, local midnight, empty/invalid, zone label`);
}
