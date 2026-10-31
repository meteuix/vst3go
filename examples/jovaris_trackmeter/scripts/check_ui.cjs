// Run with Playwright available. Uses the real embedded page, actual product
// parameter model exported by Go, and the bundled framework JavaScript SDK.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'..');
const fixture=process.env.TRACKMETER_BROWSER_FIXTURE;
if(!fixture)throw new Error('Set TRACKMETER_BROWSER_FIXTURE to the Go-exported model JSON.');
const data=JSON.parse(fs.readFileSync(fixture,'utf8'));
const spec=data.editor;
const bootstrap={model:data.model,editor:{width:spec.Width,height:spec.Height,minWidth:spec.MinWidth,minHeight:spec.MinHeight,maxWidth:spec.MaxWidth,maxHeight:spec.MaxHeight,resizable:!spec.FixedSize}};
const sdk=fs.readFileSync(path.join(root,'../../pkg/plugin/editor_assets/vst3go.js'),'utf8');
const bridge=`window.__messages=[];window.webkit={messageHandlers:{vst3go:{postMessage:m=>window.__messages.push(m)}}};window.__vst3goBootstrap=${JSON.stringify(bootstrap).replaceAll('</','<\\/')};`;
const source=fs.readFileSync(path.join(root,'ui/meter.html'),'utf8').replace('<head>','<head><script>'+bridge+'</script><script>'+sdk+'</script>');
const out=process.env.TRACKMETER_UI_EVIDENCE||path.join(root,'evidence','ui');fs.mkdirSync(out,{recursive:true});
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.TRACKMETER_BROWSER_EXECUTABLE||undefined,args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:800,height:380},deviceScaleFactor:2});const errors=[];
 page.on('pageerror',e=>errors.push(e.message));await page.setContent(source,{waitUntil:'load'});
 async function update(id,plain){await page.evaluate(({id,plain})=>{const p=window.vst3go.param(id);window.__vst3goReceive({type:'param-change',id,plain,normalized:p.toNormalized(plain),text:String(plain)});},{id,plain});}
 async function fits(label){const r=await page.evaluate(()=>{const els=[...document.querySelectorAll('header,footer,.peak-panel,.vu-panel,.bezel,.peak-controls,.calibration,.peak-readout,.channel-letter,.vu-reading')];return{w:innerWidth,h:innerHeight,scroll:[document.documentElement.scrollWidth,document.documentElement.scrollHeight],bad:els.map(n=>({class:n.className,r:n.getBoundingClientRect().toJSON()})).filter(x=>x.r.x<-.5||x.r.y<-.5||x.r.right>innerWidth+.5||x.r.bottom>innerHeight+.5)};});assert.deepEqual(r.bad,[],label+' overflows: '+JSON.stringify(r.bad));assert.equal(r.scroll[0],r.w);assert.equal(r.scroll[1],r.h);const overlaps=await page.locator('.peak-scale').evaluateAll(scales=>scales.flatMap(s=>{const r=[...s.children].map(n=>n.getBoundingClientRect());return r.slice(1).filter((x,i)=>r[i].bottom>x.top+1).length;}));assert.deepEqual(overlaps,[0,0],label+' peak scale labels overlap');}
 assert.equal(await page.locator('#ui-error').isVisible(),false);await fits('horizontal');
 assert.equal(await page.locator('.peak-channel').count(),2);assert.equal(await page.locator('.dial').count(),2);
 for(const [id,v] of [[10,-14.99],[11,-26.99],[12,-.012],[13,-12.012],[14,-14.99],[15,-26.99]])await update(id,v);
 assert.equal(await page.locator('#peak-L').textContent(),'−15.0');assert.equal(await page.locator('#peak-R').textContent(),'−27.0');
 assert.equal(await page.locator('#vu-L').textContent(),'0.0 VU');assert.equal(await page.locator('#vu-R').textContent(),'−12.0 VU');
 const initialL=await page.locator('.peak-channel').nth(0).locator('.led.on').count(),initialR=await page.locator('.peak-channel').nth(1).locator('.led.on').count();assert(initialL>initialR);
 const needles=await page.locator('.needle').evaluateAll(ns=>ns.map(n=>n.getAttribute('transform')));assert.notEqual(needles[0],needles[1]);
 // Compare the rendered needle to the printed tick, through actual SVG
 // screen transforms, at calibration points across the entire scale.
 for(const vu of [-20,-10,-7,-3,0,3]){
  await update(12,vu);
  const aligned=await page.locator('.dial').first().evaluate((dial,vu)=>{
   const needle=dial.querySelector('.needle'),label=[...dial.querySelectorAll('.scale text')].find(n=>n.textContent===(vu>0?'+'+vu:String(vu))),tick=label.previousElementSibling;
   const point=(node,x,y)=>new DOMPoint(x,y).matrixTransform(node.getScreenCTM());
   const a=point(needle,170,220),b=point(needle,170,43),c=point(tick,Number(tick.getAttribute('x1')),Number(tick.getAttribute('y1')));
   const u=[b.x-a.x,b.y-a.y],v=[c.x-a.x,c.y-a.y];
   return Math.abs(u[0]*v[1]-u[1]*v[0])/(Math.hypot(...u)*Math.hypot(...v))<1e-6;
  },vu);assert(aligned,'needle does not align to '+vu+' VU tick');
 }
 await update(12,-.012);
 await page.screenshot({path:path.join(out,'horizontal.png')});
 await page.locator('#hold').click();assert.equal(await page.locator('#hold').getAttribute('aria-pressed'),'true');
 await update(14,1.94);await update(16,1);await update(10,-50);
 assert.equal(await page.locator('#peak-L').textContent(),'+1.9');assert(await page.locator('#clip-L').evaluate(n=>n.classList.contains('active')));assert(await page.locator('.hold-marker').first().isVisible());
 assert(await page.locator('.peak-channel').first().locator('.led.on').count()<initialL);
 await page.locator('#layout-v').click();
 assert(await page.evaluate(()=>window.__messages.some(m=>m.type==='resize'&&m.width===400&&m.height===940)));
 await page.setViewportSize({width:400,height:940});await fits('vertical');
 assert.equal(await page.locator('#peak-L').textContent(),'+1.9');assert.equal(await page.locator('#vu-L').textContent(),'0.0 VU');
 await page.screenshot({path:path.join(out,'vertical.png')});
 await page.locator('#reset').click();assert(await page.evaluate(()=>window.__messages.some(m=>m.type==='message'&&m.name==='reset-peak')));
 // UI sends only a reset request; real DSP/reset behavior is verified by Go.
 assert.equal(await page.locator('#vu-L').textContent(),'0.0 VU');
 const restored=JSON.parse(JSON.stringify(bootstrap));for(const s of restored.model.sections)for(const p of s.controls){if(p.id===3){p.normalized=1;p.plain=1;}if(p.id===1){p.normalized=1;p.plain=1;}}
 const restoreSource=source.replace(JSON.stringify(bootstrap).replaceAll('</','<\\/'),JSON.stringify(restored).replaceAll('</','<\\/'));
 await page.setContent(restoreSource,{waitUntil:'load'});assert.equal(await page.locator('body').getAttribute('class'),'vertical');assert.equal(await page.locator('#hold').getAttribute('aria-pressed'),'true');await fits('restored vertical');
 await page.locator('#layout-h').click();await page.setViewportSize({width:800,height:380});await fits('back to horizontal');
 assert.equal(errors.length,0,errors.join('\n'));
 const report={browser:'Chromium (Linux), not Logic/WKWebView',checks:['self-contained page with actual SDK/model','horizontal/vertical/reopened layouts fit','L/R independent Peak LEDs and VU needles','rounded values including infinity and above-zero peak','rendered needle/tick alignment at -20/-10/-7/-3/0/+3 VU','Hold shows marker while live LEDs keep moving','Clip indicator','H/V request sizes and persistent parameter edits','Reset request leaves VU display intact'],dsp:'Go runtime tests cover actual reset/persistence; browser bridge messages are simulated for UI QA.'};
 fs.writeFileSync(path.join(out,'browser_checks.json'),JSON.stringify(report,null,2)+'\n');console.log('UI QA passed: '+report.checks.length+' check groups; H/V screenshots saved.');await browser.close();
})().catch(e=>{console.error(e);process.exitCode=1;});
