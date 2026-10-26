import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,readFileSync,copyFileSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {execFileSync} from 'node:child_process';

function fixture(t){
 const root=mkdtempSync(join(tmpdir(),'silo-catalog-release-'));mkdirSync(join(root,'scripts'));
 t.after(()=>rmSync(root,{recursive:true,force:true}));
 for(const file of ['manifest.json','checksums.txt','scripts/update-entry.mjs','scripts/validate-catalog.mjs'])copyFileSync(new URL('../'+file,import.meta.url),join(root,file));
 return root;
}
function publish(root,version){
 const prefix='dispatcharr-'+version+'-';
 const checksumPath=join(root,'release-checksums.txt');
 writeFileSync(checksumPath,['darwin-arm64','linux-amd64','linux-arm64'].map(p=>'a'.repeat(64)+'  '+prefix+p+'\n').join(''));
 execFileSync(process.execPath,[join(root,'scripts/update-entry.mjs'),'--plugin-id','silo.ramindex.dispatcharr','--repo','silo-plugin-dispatcharr','--version',version,'--asset-prefix',prefix,'--checksums',checksumPath,'--replace-old']);
}
test('a delayed older release cannot downgrade the catalog or replace its checksums',t=>{
 const root=fixture(t),manifest=readFileSync(join(root,'manifest.json'),'utf8'),checksums=readFileSync(join(root,'checksums.txt'),'utf8');
 publish(root,'0.0.0');
 assert.equal(readFileSync(join(root,'manifest.json'),'utf8'),manifest);
 assert.equal(readFileSync(join(root,'checksums.txt'),'utf8'),checksums);
});
test('a newer release still publishes its version and all platform checksums',t=>{
 const root=fixture(t);publish(root,'9999.0.0');
 const entry=JSON.parse(readFileSync(join(root,'manifest.json'),'utf8')).plugins.find(e=>e.manifest.plugin_id==='silo.ramindex.dispatcharr');
 assert.equal(entry.manifest.version,'9999.0.0');
 for(const binary of Object.values(entry.binaries)){assert.equal(binary.checksum,'a'.repeat(64));assert.ok(binary.url.includes('/v9999.0.0/'));}
});
