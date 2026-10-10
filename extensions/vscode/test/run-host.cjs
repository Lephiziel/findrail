const { runTests } = require('@vscode/test-electron');
const path = require('node:path');
(async()=>{await runTests({version:'1.95.0',extensionDevelopmentPath:path.resolve(__dirname,'..'),extensionTestsPath:path.resolve(__dirname,'host.cjs')})})().catch(e=>{console.error('extension_host_failed');console.error(e);process.exit(1)});
