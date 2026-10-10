const { runTests, downloadAndUnzipVSCode } = require('@vscode/test-electron');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs/promises');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { once } = require('node:events');
const { spawn } = require('node:child_process');

const repo=path.resolve(__dirname,'../../..');
const extensionDevelopmentPath=path.resolve(__dirname,'..');
async function waitForServer(port, child) {
 for(let i=0;i<100;i++) {
  if(child.exitCode!==null) throw new Error('compiled_server_exited');
  try {const response=await new Promise((resolve,reject)=>http.get(`http://127.0.0.1:${port}/healthz`,resolve).on('error',reject));response.resume();if(response.statusCode===200)return} catch {}
  await new Promise(r=>setTimeout(r,100));
 }
 throw new Error('compiled_server_start_timeout');
}
async function main() {
 const temp=await fs.mkdtemp(path.join(os.tmpdir(),'findrail-vscode-host-'));
 let server;
 try {
  const binary=path.join(temp,process.platform==='win32'?'findrail.exe':'findrail');
  execFileSync('go',['build','-o',binary,'./cmd/findrail'],{cwd:repo,stdio:'inherit'});
  const workspace=path.join(temp,'untrusted-workspace');await fs.mkdir(path.join(workspace,'.vscode'),{recursive:true});await fs.writeFile(path.join(workspace,'.vscode','settings.json'),JSON.stringify({'findrail.endpoint':'http://127.0.0.1:1'}));
  const docs=path.join(temp,'synthetic-docs');await fs.cp(path.join(repo,'examples/demo'),docs,{recursive:true});
  execFileSync(binary,['index','--data-dir',path.join(temp,'index'),docs],{cwd:repo,stdio:'ignore'});
  execFileSync('go',['run','./extensions/vscode/test/make_github',path.join(temp,'index')],{cwd:repo,stdio:'ignore'});
  const index=path.join(temp,'index');const filesystemSource=JSON.parse(execFileSync(binary,['sources','--data-dir',index,'--json'],{cwd:repo,encoding:'utf8'})).sources.find(source=>source.kind==='filesystem').id;
  const archive=path.join(temp,'host-synthetic.findrail.zip');execFileSync(binary,['export-source','--data-dir',index,'--source',filesystemSource,'--output',archive,'--json'],{cwd:repo,stdio:'ignore'});execFileSync(binary,['import-source','--data-dir',index,'--name','Frozen synthetic archive','--json',archive],{cwd:repo,stdio:'ignore'});
  const probe=net.createServer();probe.listen(0,'127.0.0.1');await once(probe,'listening');const port=probe.address().port;await new Promise((resolve,reject)=>probe.close(e=>e?reject(e):resolve()));
  server=spawn(binary,['serve','--data-dir',path.join(temp,'index'),'--addr',`127.0.0.1:${port}`,'--no-sync'],{cwd:repo,stdio:'ignore'});
  await waitForServer(port,server);
  process.env.FINDRAIL_TEST_ENDPOINT=`http://127.0.0.1:${port}`;
  process.env.FINDRAIL_TEST_BINARY=binary;process.env.FINDRAIL_TEST_INDEX=path.join(temp,'index');
  const testOptions={extensionDevelopmentPath,extensionTestsPath:path.resolve(__dirname,'host.cjs'),launchArgs:[workspace]};
  if(process.platform==='linux'){
   const executable=await downloadAndUnzipVSCode('1.95.0');const wrapper=path.join(temp,'vscode-test-wrapper.cjs');
   await fs.writeFile(wrapper,`#!/usr/bin/env node\nconst {spawn}=require('node:child_process');const child=spawn(${JSON.stringify(executable)},process.argv.slice(2).filter(arg=>arg!=='--disable-workspace-trust'),{stdio:'inherit',env:{...process.env,FINDRAIL_TEST_RESTRICTED:'1'}});child.on('error',e=>{console.error(e);process.exit(1)});child.on('exit',(code,signal)=>process.exit(code??(signal?1:0)));\n`);
   await fs.chmod(wrapper,0o755);testOptions.vscodeExecutablePath=wrapper;
  } else testOptions.version='1.95.0';
  await runTests(testOptions);
 } finally {
  delete process.env.FINDRAIL_TEST_ENDPOINT;
  delete process.env.FINDRAIL_TEST_BINARY;delete process.env.FINDRAIL_TEST_INDEX;
  if(server&&server.exitCode===null&&server.signalCode===null){const exited=once(server,'exit');server.kill();await Promise.race([exited,new Promise(r=>setTimeout(r,3000))])}
  await fs.rm(temp,{recursive:true,force:true});
 }
}
main().catch(e=>{console.error('extension_host_failed');console.error(e);process.exit(1)});
