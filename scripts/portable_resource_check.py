#!/usr/bin/env python3
"""Small per-document v1 boundary spot check; not a scale benchmark."""
import json
import pathlib
import platform
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True

def run(binary, *args):
    result=subprocess.run([str(binary),*map(str,args)],capture_output=True,text=True,encoding='utf-8',timeout=120)
    if result.returncode:raise RuntimeError((args,result.stdout,result.stderr))
    return result


def main():
    binary=pathlib.Path(sys.argv[1]).resolve();size=8<<20
    pattern=bytes(range(32,127));payload=(pattern*((size+len(pattern)-1)//len(pattern)))[:size]
    with tempfile.TemporaryDirectory(prefix='findrail-portable-resource-') as temp:
        base=pathlib.Path(temp);root=base/'near-limit';root.mkdir();(root/'boundary.txt').write_bytes(payload)
        data=base/'index';run(binary,'index','--data-dir',data,'--max-bytes',10<<20,'--max-pdf-bytes','0','--max-docx-bytes','0',root)
        source=json.loads(run(binary,'sources','--data-dir',data,'--json').stdout)['sources'][0]['id']
        output=base/'near-limit.zip';start=time.monotonic();proc=subprocess.Popen([str(binary),'export-source','--data-dir',str(data),'--source',source,'--output',str(output),'--json'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8')
        peak=None
        while proc.poll() is None:
            try:
                for line in pathlib.Path(f'/proc/{proc.pid}/status').read_text().splitlines():
                    if line.startswith('VmHWM:'):peak=int(line.split()[1])*1024
            except (OSError,ValueError):pass
            time.sleep(.02)
        stdout,stderr=proc.communicate(timeout=5);elapsed=time.monotonic()-start
        if proc.returncode:raise RuntimeError((stdout,stderr))
        result=json.loads(stdout)
        print(json.dumps({'platform':platform.platform(),'toolchain':subprocess.run(['go','version'],capture_output=True,text=True).stdout.strip(),
            'document_text_bytes':size,'archive_bytes':result['archive_bytes'],'elapsed_seconds':round(elapsed,3),
            'peak_rss_bytes':peak,'cancellation':'unit-tested via canceled context; CLI interruption removes private staging and publishes no target'},sort_keys=True))


if __name__=='__main__':main()
