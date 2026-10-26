import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
const script=fs.readFileSync(new URL('./media.js',import.meta.url),'utf8');
function fixture(options={},Hls){
 const requests=[];let start,stop;
 const video={pause(){},load(){},removeAttribute(){},canPlayType(){return true},play(){return Promise.resolve()}};
 const ctx=vm.createContext({window:{Hls},Hls,URL,location:{href:'https://silo.example/plugin'},setTimeout,clearTimeout,setInterval:()=>1,clearInterval(){},fetch:async(path,options)=>{
  requests.push([path,options]);
  if(path.endsWith('/start'))return new Promise(resolve=>{start=data=>resolve({ok:true,json:async()=>data})});
  if(path.endsWith('/stop'))return new Promise(resolve=>{stop=()=>resolve({ok:true,json:async()=>({})})});
  return {ok:true,json:async()=>({state:'ready'})};
 }});vm.runInContext(script,ctx);
 const media=new ctx.window.SiloLiveMedia({video,base:'/test',root:'',headers:()=>({}),loadLibraries:async()=>{},...options});
 return {media,requests,get start(){return start},get stop(){return stop}};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('stopped pending start releases its late server reservation',async()=>{
 const f=fixture();const playing=f.media.play('one');await tick();assert.ok(f.start);
 const stopping=f.media.stop();await stopping;
 f.start({sessionId:'old',path:'/test/relay/media',container:'hls'});await playing;
 assert.ok(f.requests.some(([p,o])=>p.endsWith('/stop')&&JSON.parse(o.body).sessionId==='old'));
 f.stop();
});
test('a stop while an earlier stop is pending cannot revive playback',async()=>{
 const f=fixture();f.media.session='old';
 const playing=f.media.play('one');await tick();
 await f.media.stop();f.stop();await playing;
 assert.equal(f.requests.filter(([p])=>p.endsWith('/start')).length,0);
});

test('source HLS tuning applies to direct playback while converted and rewind playlists keep their own window',async()=>{
 for(const mode of ['direct','compatible','rewind']) {
  let config;
  class Hls {
   static isSupported(){return true};static Events={ERROR:'error'};
   constructor(options){config=options};on(){};loadSource(){};attachMedia(){};destroy(){}
  }
  const f=fixture({hlsOptions:{liveSyncDuration:24,liveMaxLatencyDuration:48,maxBufferLength:48}},Hls);
  const playing=f.media.play('one',mode);await tick();f.start({sessionId:'play',path:'/test/media',container:'hls'});await playing;
  assert.equal(config.liveSyncDuration,mode==='direct'?24:6);
  assert.equal(config.liveMaxLatencyDuration,mode==='direct'?48:mode==='rewind'?Infinity:18);
  const stopped=f.media.stop();f.stop();await stopped;
 }
});
