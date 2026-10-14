// Run: npm install --no-save --package-lock=false jsdom@26
//      node --test tests/browser-ui.test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');
const assets = path.join(__dirname, '../cmd/chitchatui/frontend/dist');
function setup(api = {}) {
  const html = fs.readFileSync(path.join(assets, 'index.html'), 'utf8');
  const dom = new JSDOM(html, { runScripts: 'outside-only', url: 'http://localhost/' });
  const w = dom.window;
  // Test the actual script without starting its unrelated bootstrap/timer.
  w.addEventListener = () => {};
  w.go = { main: { App: api } };
  w.eval(fs.readFileSync(path.join(assets, 'app.js'), 'utf8'));
  const options = [{key:'rod',label:'Rod',description:'Rod capture'}, {key:'chromedp',label:'ChromeDP',description:'CDP capture'}, {key:'show_mode',label:'Show Mode',description:'Visible Rod capture'}];
  w.renderBrowserOptions(options);
  return { dom, w, d:w.document, options };
}
const plain = x => JSON.parse(JSON.stringify(x));
test('numbered list, count, per-row Run and no old save/radio/central button', () => {
  const {dom,d} = setup();
  assert.equal(d.querySelector('#browser-available').textContent, 'Available: 3');
  assert.deepEqual([...d.querySelectorAll('.browser-number')].map(x=>x.textContent), ['01','02','03']);
  assert.equal(d.querySelectorAll('.browser-run').length, 3);
  assert.equal(d.querySelectorAll('[name="browser-backend"], #run-fresh-browsers, #browser-form [type="submit"]').length, 0);
  assert.equal(d.querySelector('#browser-status').textContent, 'Status: Ready');
  dom.window.close();
});
test('clicked ChromeDP sends live inputs, prevents overlapping clicks, and leaves form edits intact', async () => {
  let resolve; const calls = [];
  const {dom,w,d,options} = setup({RunBrowser:(...args)=>{calls.push(plain(args));return new Promise(r=>resolve=r);}});
  d.querySelector('#browser-profile').value = ' new-profile ';
  d.querySelector('#browser-headless').checked = true;
  d.querySelector('#fresh-browser-count').value = '3';
  const pending = w.runBrowserOption(options[1]);
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>b.disabled));
  await w.runBrowserOption(options[0]);
  assert.deepEqual(calls, [[{backend:'chromedp',profile:'new-profile',headless:true},3]]);
  d.querySelector('#browser-profile').value = 'edited-while-pending';
  w.renderBrowserOptions(options); // refreshing must preserve pending button state
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>b.disabled));
  resolve(); await pending;
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>!b.disabled));
  assert.equal(d.querySelector('#browser-profile').value, 'edited-while-pending');
  assert.match(d.querySelector('#browser-status').textContent,/request accepted/);
  dom.window.close();
});
test('each row button dispatches its own backend; blank profile defaults', async () => {
  const calls=[]; const {dom,d} = setup({RunBrowser:async (...a)=>calls.push(plain(a))});
  for(const button of d.querySelectorAll('.browser-run')) {
    button.click(); await new Promise(r=>setImmediate(r));
  }
  assert.deepEqual(calls.map(a=>a[0].backend),['rod','chromedp','show_mode']);
  assert.equal(calls[0][0].profile,'default');
  dom.window.close();
});
test('invalid count never invokes backend', async () => {
  let calls=0; const {dom,w,d,options}=setup({RunBrowser:async()=>calls++});
  for(const value of ['', '0','6','1.5','-1']) {
    d.querySelector('#fresh-browser-count').value=value;
    await w.runBrowserOption(options[0]);
    assert.match(d.querySelector('#browser-status').textContent,/between 1 and 5/);
  }
  assert.equal(calls,0); dom.window.close();
});
test('backend failure and unavailable binding show errors and restore buttons', async () => {
  const {dom,w,d,options}=setup({RunBrowser:async()=>{throw new Error('Proxy is invalid');}});
  await w.runBrowserOption(options[0]);
  assert.match(d.querySelector('#browser-status').textContent,/Proxy is invalid/);
  assert.ok(d.querySelector('#browser-status').classList.contains('browser-error'));
  assert.ok([...d.querySelectorAll('.browser-run')].every(b=>!b.disabled));
  delete w.go.main.App.RunBrowser;
  await w.runBrowserOption(options[0]);
  assert.match(d.querySelector('#browser-status').textContent,/Wails binding not ready/);
  dom.window.close();
});
test('fresh-browser worker status wins over late request acknowledgement', async () => {
  let resolve; const events={}; const {dom,w,d,options}=setup({RunBrowser:()=>new Promise(r=>resolve=r)});
  w.runtime={EventsOn:(name,fn)=>events[name]=fn};
  w.bindEvents();
  const pending=w.runBrowserOption(options[1]);
  events['ui:log']({level:'error',message:'[default fresh 1/1] Browser start failed: test',created_at:''});
  resolve(); await pending;
  assert.match(d.querySelector('#browser-status').textContent,/Browser start failed/);
  events['ui:log']({level:'info',message:'Unrelated chat activity',created_at:''});
  assert.match(d.querySelector('#browser-status').textContent,/Browser start failed/);
  dom.window.close();
});
test('state refresh initializes browser defaults only once', () => {
  const {dom,w,d,options}=setup();
  const snapshot={browser:{profile:'saved',headless:false},browser_options:options,message:{},proxy:{},general:{},home:{}};
  w.fillForms(snapshot);
  d.querySelector('#browser-profile').value='draft';
  d.querySelector('#browser-headless').checked=true;
  d.querySelector('#fresh-browser-count').value='5';
  w.fillForms(snapshot);
  assert.equal(d.querySelector('#browser-profile').value,'draft');
  assert.equal(d.querySelector('#browser-headless').checked,true);
  assert.equal(d.querySelector('#fresh-browser-count').value,'5');
  dom.window.close();
});
test('browser form submit is prevented and does not choose a backend', () => {
  let calls=0; const {dom,w,d}=setup({RunBrowser:()=>calls++});
  w.bindForms();
  const event=new w.Event('submit',{cancelable:true});
  d.querySelector('#browser-form').dispatchEvent(event);
  assert.ok(event.defaultPrevented); assert.equal(calls,0);
  dom.window.close();
});
test('backend labels and descriptions are escaped', () => {
  const {dom,w,d}=setup();
  w.renderBrowserOptions([{key:'rod',label:'<img src=x>',description:'<script>bad()</script>'}]);
  assert.equal(d.querySelector('#browser-options img'),null);
  assert.equal(d.querySelector('#browser-options script'),null);
  assert.equal(d.querySelector('#browser-available').textContent,'Available: 1');
  dom.window.close();
});

test('routine cleanup does not hide the login result or launch failure', () => {
  const events={}; const {dom,w,d}=setup();
  w.runtime={EventsOn:(name,fn)=>events[name]=fn}; w.bindEvents();
  for(const message of ['Browser start failed: test','Login saved to test.json']) {
    events['ui:log']({level:message.startsWith('Browser')?'error':'info',message:`[default fresh 1/1] ${message}`,created_at:''});
    events['ui:log']({level:'info',message:'[default fresh 1/1] Removed temporary browser data from temporary-test-path',created_at:''});
    assert.ok(d.querySelector('#browser-status').textContent.includes(message));
  }
  dom.window.close();
});

test('Show Mode always requests a visible browser without changing the Headless preference', async () => {
  const calls=[]; const {dom,w,d,options}=setup({RunBrowser:async(...args)=>calls.push(plain(args))});
  d.querySelector('#browser-headless').checked=true;
  d.querySelector('#browser-profile').value='visible-test';
  d.querySelector('#fresh-browser-count').value='2';
  await w.runBrowserOption(options[2]);
  assert.deepEqual(calls[0],[{backend:'show_mode',profile:'visible-test',headless:false},2]);
  assert.equal(d.querySelector('#browser-headless').checked,true);
  await w.runBrowserOption(options[0]);
  assert.equal(calls[1][0].headless,true);
  dom.window.close();
});
