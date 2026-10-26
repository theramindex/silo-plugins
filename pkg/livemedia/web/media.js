/* Shared Live TV attachment lifecycle. Source URLs never enter the browser. */
window.SiloLiveMedia = class {
  constructor(options) { this.options = options; this.generation = 0; }
  async request(path, method, body) {
    const response = await (this.options.fetch || fetch)(this.options.base + path, {method: method || 'GET', credentials: 'include', headers: {...this.options.headers(), ...(body ? {'Content-Type':'application/json'} : {})}, body:body ? JSON.stringify(body) : undefined});
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || 'Playback is temporarily unavailable.');
    return data;
  }
  clearDecoder() {
    if (this.hls) this.hls.destroy(); if (this.ts) this.ts.destroy();
    this.hls = null; this.ts = null;
    const video=this.options.video; video.onerror=null;video.onended=null;video.onplaying=null; video.pause();video.removeAttribute('src');video.load();
  }
  async stop() {
    this.generation++;clearInterval(this.timer);this.timer=null;this.clearDecoder();
    const id=this.session;this.session=null;
    if (id) await this.request('/api/media/stop','POST',{sessionId:id}).catch(()=>{});
  }
  async play(channel, mode='direct', rewind=false) {
    const stopping=this.stop();const generation=this.generation;await stopping;if(generation!==this.generation)return;this.channel=channel;this.mode=mode;this.rewind=rewind;
    const active=()=>generation===this.generation;
    const status=message=>{if(active())this.options.onStatus?.(message)};
    try {
      status(mode==='direct'?'Opening channel…':'Preparing '+(mode==='rewind'?'Live Rewind':'compatible playback')+'…');
      const session=await this.request('/api/media/start','POST',{channelId:channel,mode,rewind,reservationId:this.options.reservationId||""});
      if(!active()){this.request('/api/media/stop','POST',{sessionId:session.sessionId}).catch(()=>{});return}
      this.session=session.sessionId;
      this.timer=setInterval(()=>this.request('/api/media/status?session_id='+encodeURIComponent(session.sessionId)).catch(error=>{if(active())this.fail(error)}),20000);
      if(mode!=='direct') {
        let ready=false;
        for(let poll=0;poll<35&&active();poll++) {
          const state=await this.request('/api/media/status?session_id='+encodeURIComponent(session.sessionId));
          if(state.state==='failed')throw new Error(state.error||'The channel could not be prepared.');
          if(state.state==='ready'){ready=true;break}
          await new Promise(resolve=>setTimeout(resolve,1000));
        }
        if(!active())return;if(!ready)throw new Error('The channel took too long to prepare.');
      }
      await this.options.loadLibraries();if(!active())return;
      const video=this.options.video,url=this.options.root+session.path;
      const fatal=error=>{
        if(!active())return;
        const codec=error?.code===3||error?.code===4||error?.type==='mediaError'||error?.type==='MediaError'||/codec|unsupported.*format/i.test(error?.detail||'');
        if(codec&&mode!=='compatible'){this.play(channel,'compatible',mode==='rewind'||rewind);return}
        this.fail(new Error('This channel could not play. Try another source or try again.'));
      };
      video.onerror=()=>{if(video.error)fatal({code:video.error.code})};video.onplaying=()=>status('');video.onended=()=>{if(active()){this.stop();this.options.onStatus?.('Playback ended.')}};
      const hls=mode!=='direct'||session.container==='hls';
      if(hls&&window.Hls?.isSupported()) {
        this.hls=new Hls({backBufferLength:60,maxBufferLength:30,liveSyncDuration:6,liveMaxLatencyDuration:rewind||mode==='rewind'?Infinity:18,xhrSetup:(xhr,url)=>{if(new URL(url,location.href).origin!==location.origin)throw new Error('Unapproved media origin');for(const[k,v]of Object.entries(this.options.headers()))xhr.setRequestHeader(k,v);xhr.withCredentials=true;}});
        this.hls.on(Hls.Events.ERROR,(_,data)=>{if(data?.fatal)fatal(data)});this.hls.loadSource(url);this.hls.attachMedia(video);
      } else if(hls&&video.canPlayType('application/vnd.apple.mpegurl')) video.src=url;
      else if(window.mpegts?.isSupported()) {
        this.ts=mpegts.createPlayer({type:'mpegts',isLive:true,url},{enableWorker:false,enableStashBuffer:false,lazyLoad:false,headers:this.options.headers()});
        this.ts.on(mpegts.Events.ERROR,(type,detail)=>fatal({type,detail}));this.ts.attachMediaElement(video);this.ts.load();
      } else {fatal({code:4});return}
      video.play().catch(()=>status('Press Play to start.'));
      this.options.onAttached?.(this);
    } catch(error) {
      if(!active())return;
      if(mode==='rewind'){this.play(channel,'compatible',true);return}
      this.fail(error);
    }
  }
  fail(error){this.options.onError?.(error.message);this.stop();}
};

// Shared editor for explicit station alternatives. Channel names are labels;
// only saved channel IDs and station keys establish equivalence.
window.SiloLiveMedia.stationEditor = function(root, settings, channels, onChange) {
  root.replaceChildren();
  const heading=document.createElement('h3');heading.textContent='Station alternatives';
  const help=document.createElement('p');help.textContent='Give equivalent feeds the same station key. Lower priority numbers play first; another mapped feed is tried if the source cannot start.';
  root.append(heading,help);
  const rows=Array.isArray(settings.stationMappings)?settings.stationMappings:[];
  const list=document.createElement('div');list.className='station-mapping-list';root.append(list);
  for(const row of rows) {
    const line=document.createElement('div');line.className='settings-row compact-row';
    const text=document.createElement('span');text.textContent=(channels.find(c=>c.id===row.channelId)?.name||row.channelId)+' · '+row.stationId+' · priority '+row.priority;
    const remove=document.createElement('button');remove.type='button';remove.textContent='Remove';remove.setAttribute('aria-label','Remove mapping for '+text.textContent);
    remove.onclick=()=>{settings.stationMappings=rows.filter(r=>r.channelId!==row.channelId);onChange(settings.stationMappings);window.SiloLiveMedia.stationEditor(root,settings,channels,onChange);};
    line.append(text,remove);list.append(line);
  }
  if(!rows.length){const empty=document.createElement('p');empty.textContent='No station alternatives configured.';list.append(empty);}
  const form=document.createElement('div');form.className='station-mapping-form';
  const field=(title,type)=>{const label=document.createElement('label');label.textContent=title;const input=document.createElement('input');input.type=type;label.append(input);form.append(label);return input;};
  const channel=field('Channel (search by name or ID)','text');channel.autocomplete='off';
  const choices=document.createElement('datalist');choices.id='station-choices-'+Math.random().toString(36).slice(2);channel.setAttribute('list',choices.id);form.append(choices);
  channel.oninput=()=>{choices.replaceChildren();const query=channel.value.toLowerCase();for(const item of channels.filter(c=>(c.name+' '+c.id).toLowerCase().includes(query)).slice(0,30)){const option=document.createElement('option');option.value=item.id;option.label=item.name;choices.append(option);}};
  const station=field('Station key','text');station.maxLength=64;station.placeholder='For example: msg-network';
  const priority=field('Priority','number');priority.min='-1000';priority.max='1000';priority.value='0';
  const add=document.createElement('button');add.type='button';add.textContent='Add station mapping';
  const notice=document.createElement('p');notice.setAttribute('role','status');
  add.onclick=()=>{
    const value=channel.value.trim(),key=station.value.trim();
    if(!channels.some(c=>c.id===value)||!/^[-a-zA-Z0-9_]{1,64}$/.test(key)){notice.textContent='Choose a channel from the suggestions and enter a station key using letters, numbers, underscores, or hyphens.';return;}
    settings.stationMappings=[...rows.filter(r=>r.channelId!==value),{channelId:value,stationId:key,priority:Math.max(-1000,Math.min(1000,Number(priority.value)||0))}];
    onChange(settings.stationMappings);window.SiloLiveMedia.stationEditor(root,settings,channels,onChange);
  };
  form.append(add,notice);root.append(form);
};
