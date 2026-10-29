#!/usr/bin/env python3
"""Measure document backup load in disposable Linux Docker containers (512 MiB).

Uses an existing node:24-bookworm-slim image, standard-library host HTTP clients,
valid generated PDF/PNG/TXT and one repository JPEG. Never reads household data.
"""
import argparse
import base64
import concurrent.futures
import hashlib
import http.cookiejar
import http.client
import json
from pathlib import Path
import random
import re
import shutil
import socket
import statistics
import struct
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid
import zlib

MIB = 1 << 20
PASSWORD = 'Disposable backup load password 2026'
SAMPLER = r'''
const fs=require('node:fs');
function sample() {
 const status=fs.readFileSync('/proc/1/status','utf8');
 const stat=fs.readFileSync('/proc/1/stat','utf8').split(' ');
 const memory=Object.fromEntries(fs.readFileSync('/sys/fs/cgroup/memory.stat','utf8').trim().split('\n').map(line=>{const [key,value]=line.split(' ');return [key,Number(value)]}));
 const events=Object.fromEntries(fs.readFileSync('/sys/fs/cgroup/memory.events','utf8').trim().split('\n').map(line=>{const [key,value]=line.split(' ');return [key,Number(value)]}));
 console.log(JSON.stringify({epoch_ms:Date.now(),rss_bytes:Number(status.match(/^VmRSS:\s+(\d+)/m)[1])*1024,cgroup_bytes:Number(fs.readFileSync('/sys/fs/cgroup/memory.current','utf8')),cpu_ticks:Number(stat[13])+Number(stat[14]),anon_bytes:memory.anon,file_bytes:memory.file,socket_bytes:memory.sock,memory_events:events}));
}
sample(); const timer=setInterval(sample,50);
process.stdin.resume(); process.stdin.on('end',()=>{clearInterval(timer);process.exit(0)});
'''


def docker(*args):
    return subprocess.check_output(['docker', *args], text=True, stderr=subprocess.STDOUT).strip()


def png(width, height):
    rng = random.Random(20261023)
    raw = b''.join(b'\0' + rng.randbytes(width * 3) for _ in range(height))
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>2I5B', width, height, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b''))


def pdf():
    content = b'BT /F1 14 Tf 50 760 Td (HomeAtlas - household maintenance guide) Tj 0 -24 Td (Generated load fixture. No personal data.) Tj ET\n'
    objects = [b'<< /Type /Catalog /Pages 2 0 R >>',
               b'<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
               b'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
               b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
               b'<< /Length ' + str(len(content)).encode() + b' >>\nstream\n' + content + b'endstream']
    data = b'%PDF-1.4\n'
    offsets = [0]
    for index, obj in enumerate(objects, 1):
        offsets.append(len(data))
        data += f'{index} 0 obj\n'.encode() + obj + b'\nendobj\n'
    xref = len(data)
    data += b'xref\n0 6\n0000000000 65535 f \n' + b''.join(f'{offset:010} 00000 n \n'.encode() for offset in offsets[1:])
    return data + f'trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n'.encode()


def summarize(values):
    if not values:
        return None
    ordered = sorted(values)
    return {'count': len(values), 'median': statistics.median(values),
            'p95': ordered[min(len(ordered)-1, int(.95*len(ordered)))], 'max': max(values)}




def large_post(origin, path, body, headers, *, timeout=130):
    # Keep the connection so an early HTTP rejection is readable even when the
    # peer stops consuming a large upload (urllib otherwise hides it as EPIPE).
    parsed = urllib.parse.urlsplit(origin)
    connection = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=timeout)
    before = time.perf_counter()
    send_error = None
    try:
        try:
            connection.request('POST',path,body,headers)
        except (BrokenPipeError, ConnectionResetError) as err:
            send_error = type(err).__name__
        response = connection.getresponse()
        content = response.read()
        return {'status':response.status,'duration_ms':(time.perf_counter()-before)*1000,
                'bytes':len(content),'send_error':send_error}, content
    finally:
        connection.close()


def reader_summary(records, phases):
    restores = [(p['start_epoch_ms'],p['end_epoch_ms']) for p in phases if p['name'].endswith('-restore') or p['name']=='restore']
    groups = {'all':[], 'export_without_restore':[], 'overlapping_restore':[], 'other':[]}
    errors = []
    for row in records:
        if 'error' in row:
            errors.append(row)
            continue
        duration = row['duration_ms']
        overlap = any(row['start_epoch_ms'] < end and row['end_epoch_ms'] > start for start,end in restores)
        group = 'overlapping_restore' if overlap else ('export_without_restore' if row['started_during'].endswith('-backup') else 'other')
        groups['all'].append(duration)
        groups[group].append(duration)
    return {'latency_ms':{key:summarize(values) for key,values in groups.items()},'errors':errors,'count':len(records)}


def gc_cycles(log):
    cycles = []
    for line in log.splitlines():
        match = re.match(r'gc (\d+) @([\d.]+)s .*?: ([\d.]+)\+([\d.]+)\+([\d.]+) ms clock, (.*?) ms cpu,', line)
        if match:
            stw1, mark, stw2 = map(float, match.group(3, 4, 5))
            # gctrace CPU terms can include slash-separated assist/background/idle.
            cpu = sum(float(value) for value in re.findall(r'[\d.]+', match.group(6)))
            cycles.append({'cycle': int(match[1]), 'uptime_s': float(match[2]), 'forced': '(forced)' in line,
                           'stw_ms': stw1+stw2, 'cycle_clock_ms': stw1+mark+stw2, 'cpu_ms': cpu})
    return cycles


def recover_case(binary, output, label, image, storage_plugin, archive, documents, storage_records, io_options, storage_shared=False, restricted=False):
    """A second empty installation, its own 512 MiB cgroup and approved package."""
    storage_id = 'ext-shared-load' if storage_shared else 'ext-storage-load'
    permissions = ','.join(storage_id+'.'+p for p in (['read','write','share','storage','storage.shared'] if storage_shared else ['read','write','storage']))
    name = 'homeatlas-recovery-' + uuid.uuid4().hex[:12]
    volume = name + '-data'
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        port = sock.getsockname()[1]
    origin = f'http://127.0.0.1:{port}'
    def client():
        return urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    owner,member = client(),client()
    request_timeout = 300 if io_options else 130
    def request(path, body=None, who=owner):
        data = json.dumps(body).encode() if body is not None else None
        response = who.open(urllib.request.Request(origin+path,data=data,headers={'Origin':origin,'Content-Type':'application/json'}),timeout=request_timeout)
        return response.status,response.read()
    result = {'completed':False,'phases':[]}
    sampler = None
    created = False
    samples = (output/f'{label}-recovery-samples.jsonl').open('w')
    try:
        docker('volume','create',volume)
        digest = hashlib.sha256(storage_plugin.read_bytes()).hexdigest()
        docker('run','--rm','--pull=never','--memory=512m','--memory-swap=512m','--read-only',
               '--mount',f'type=volume,source={volume},target=/data','-v',f'{binary}:/homeatlas:ro',
               '-v',f'{storage_plugin}:/plugin.hap:ro','--entrypoint','/homeatlas',image,
               'plugins','install','-data','/data','-file','/plugin.hap','-sha256',digest,
               '-allow',permissions)
        docker('run','-d','--pull=never','--name',name,'--memory=512m','--memory-swap=512m','--read-only',*io_options,
               '--mount',f'type=volume,source={volume},target=/data','--tmpfs','/tmp:rw,noexec,nosuid,size=32m',
               '-p',f'127.0.0.1:{port}:8080','-v',f'{binary}:/homeatlas:ro','-e','GODEBUG=gctrace=1','--entrypoint','/homeatlas',image,
               '-addr','0.0.0.0:8080','-public-url',origin,'-data','/data')
        created = True
        result['kernel_limits'] = {k:docker('exec',name,'cat',f'/sys/fs/cgroup/{k}') for k in ('memory.max','memory.swap.max','io.max')}
        assert int(result['kernel_limits']['memory.max'])==512*MIB and int(result['kernel_limits']['memory.swap.max'])==0
        for _ in range(100):
            try:
                if request('/api/health')[0]==200:
                    break
            except (OSError,urllib.error.URLError):
                pass
            time.sleep(.1)
        else:
            raise RuntimeError('recovery server did not start')
        sampler = subprocess.Popen(['docker','exec','-i',name,'node','-e',SAMPLER],stdin=subprocess.PIPE,stdout=samples,stderr=subprocess.PIPE)
        code = docker('exec',name,'cat','/data/setup-code')
        before = time.time()*1000
        response,content = large_post(origin,'/api/setup/restore',archive.read_bytes(),{
            'Origin':origin,'Content-Type':'application/octet-stream','X-HomeAtlas-Setup-Code':code,
            'X-HomeAtlas-Confirm-Restore':'initialize-installation','X-HomeAtlas-Backup-Password':base64.b64encode(PASSWORD.encode()).decode()},timeout=request_timeout)
        assert response['status']==201,(response,content)
        result['phases'].append({'name':'recover-empty','start_epoch_ms':before,'end_epoch_ms':time.time()*1000,**response})
        assert request('/api/auth/login',{'identifier':'load-fixture','password':PASSWORD})[0]==200
        assert request('/api/auth/login',{'identifier':'storage-member@example.test','password':PASSWORD},member)[0]==200
        before = time.time()*1000
        for who,seed in ((owner,1),(member,2)):
            for start in range(0,storage_records//2,32):
                count = min(32,storage_records//2-start)
                status,content = request(f'/api/{storage_id}/actions/check',{'start':str(start),'count':str(count),'bytes':'16384','seed':str(seed)},who)
                assert status==200 and json.loads(content)['text']==f'check {count}',(status,content)
        if storage_shared:
            for who,first in ((owner,0),(member,1 if restricted else 0)):
                for start in range(first,1024,32):
                    count = min(32,1024-start)
                    status,content = request(f'/api/{storage_id}/actions/shared_check',{'start':str(start),'count':str(count),'bytes':'16384','seed':'3'},who)
                    assert status==200 and json.loads(content)['text']==f'shared_check {count}',(status,content)
            if restricted:
                status,content = request(f'/api/{storage_id}/actions/shared_missing',{'start':'0','count':'1','bytes':'16384','seed':'3'},member)
                assert status==200,(status,content)
                try:
                    request(f'/api/{storage_id}/actions/shared_write',{'start':'1','count':'1','bytes':'16384','seed':'3'},member)
                except urllib.error.HTTPError as err:
                    assert err.code==403
                else:
                    raise AssertionError('recovered read grant allowed writing')
        for document in documents:
            status,content = request(f'/api/documents/{document["id"]}/download')
            assert status==200 and hashlib.sha256(content).hexdigest()==document['sha256']
        result['phases'].append({'name':'verify-recovered','start_epoch_ms':before,'end_epoch_ms':time.time()*1000,'records_verified':storage_records,'shared_records_verified':1024 if storage_shared else 0,'shared_recipient_restricted':restricted if storage_shared else None,'originals_verified':len(documents)})
        result['memory_events'] = docker('exec',name,'cat','/sys/fs/cgroup/memory.events')
        result['cgroup_peak_bytes'] = int(docker('exec',name,'cat','/sys/fs/cgroup/memory.peak'))
        assert re.search(r'^oom_kill 0$',result['memory_events'],re.M) and re.search(r'^oom 0$',result['memory_events'],re.M)
        result['completed'] = True
    except BaseException as err:
        result['failure'] = f'{type(err).__name__}: {err}'
        raise
    finally:
        if sampler:
            sampler.stdin.close();sampler.wait(timeout=10)
            result['sampler_returncode'] = sampler.returncode
        samples.close()
        if created:
            logs = subprocess.run(['docker','logs',name],text=True,capture_output=True)
            (output/f'{label}-recovery-server.log').write_text(logs.stdout+logs.stderr)
            result['final_state'] = json.loads(docker('inspect','--format','{{json .State}}',name))
            docker('rm','-f',name)
        docker('volume','rm',volume)
        (output/f'{label}-recovery-result.json').write_text(json.dumps(result,indent=2)+'\n')
    return result


def run_case(binary, output, label, corpus, image, rounds, *, soak_seconds=0, readers=0,
             io_device=None, read_bps=0, write_bps=0, cycle_idle=2, tools_plugin=None, storage_plugin=None, storage_records=4096,recover_new=False,storage_shared=False):
    storage_id = 'ext-shared-load' if storage_shared else 'ext-storage-load'
    storage_permissions = ','.join(storage_id+'.'+p for p in (['read','write','share','storage','storage.shared'] if storage_shared else ['read','write','storage']))
    live_acl_restricted = archive_acl_restricted = False
    name = 'homeatlas-load-' + uuid.uuid4().hex[:12]
    volume = name + '-data'
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    origin = f'http://127.0.0.1:{port}'
    request_timeout = 300 if io_device else 130
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    result = {'case': label, 'document_count': len(corpus), 'original_bytes': sum(len(data) for _, data in corpus), 'phases': []}
    created = False
    sampler = None
    stop_readers = threading.Event()
    reader_threads = []
    reader_errors = []
    reader_counts = {}
    reader_lock = threading.Lock()
    active_phase = 'setup'
    reader_file = (output / f'{label}-readers.jsonl').open('w')
    sample_path = output / f'{label}-samples.jsonl'
    sample_file = sample_path.open('w')
    archive_dir = tempfile.TemporaryDirectory(prefix='homeatlas-load-archives-')
    def request(path, body=None, headers=None, save=None, client=None):
        before = time.perf_counter()
        req = urllib.request.Request(origin+path, data=body, headers={'Origin': origin, **(headers or {})})
        try:
            response = (client or opener).open(req, timeout=request_timeout)
        except urllib.error.HTTPError as err:
            response = err
        with response:
            if save and response.status == 200:
                with save.open('wb') as dst:
                    shutil.copyfileobj(response, dst, 64 << 10)
                content = b''
                size = save.stat().st_size
            else:
                content = response.read()
                size = len(content)
            return {'status': response.status, 'duration_ms': (time.perf_counter()-before)*1000, 'bytes': size}, content
    def phase(label, work):
        nonlocal active_phase
        active_phase = label
        start = time.time()*1000
        gc_before = len(gc_cycles(docker('logs', name)))
        events_before = dict(line.split() for line in docker('exec', name, 'cat', '/sys/fs/cgroup/memory.events').splitlines())
        value = work()
        end = time.time()*1000
        cycles = gc_cycles(docker('logs', name))[gc_before:]
        events_after = dict(line.split() for line in docker('exec', name, 'cat', '/sys/fs/cgroup/memory.events').splitlines())
        entry = {'name': label, 'start_epoch_ms': start, 'end_epoch_ms': end, 'results': value, 'gc_cycles': cycles, 'memory_events_delta': {key: int(events_after[key])-int(value) for key,value in events_before.items()}}
        result['phases'].append(entry)
        (output/f'{result["case"]}-result.json').write_text(json.dumps(result,indent=2)+'\n')
        active_phase = 'between-phases'
        print(f'{result["case"]}: {label} complete', flush=True)
        return value
    try:
        docker('volume', 'create', volume)
        for package, permissions in [(tools_plugin, 'ext-household-tools.read,ext-household-tools.write'),
                                     (storage_plugin, storage_permissions)]:
            if not package:
                continue
            digest = hashlib.sha256(package.read_bytes()).hexdigest()
            docker('run', '--rm', '--pull=never', '--memory=512m', '--memory-swap=512m', '--read-only',
                   '--mount', f'type=volume,source={volume},target=/data', '-v', f'{binary}:/homeatlas:ro',
                   '-v', f'{package}:/plugin.hap:ro', '--entrypoint', '/homeatlas', image,
                   'plugins', 'install', '-data', '/data', '-file', '/plugin.hap', '-sha256', digest,
                   '-allow', permissions)
            result.setdefault('plugins',[]).append({'package_sha256':digest,'permissions':permissions})
        io_options = []
        if io_device:
            io_options = ['--device-read-bps', f'{io_device}:{read_bps}', '--device-write-bps', f'{io_device}:{write_bps}']
        docker('run', '-d', '--pull=never', '--name', name, '--memory=512m', '--memory-swap=512m', '--read-only', *io_options,
               '--mount', f'type=volume,source={volume},target=/data', '--tmpfs', '/tmp:rw,noexec,nosuid,size=32m',
               '-p', f'127.0.0.1:{port}:8080', '-v', f'{binary}:/homeatlas:ro',
               '-e', 'GODEBUG=gctrace=1', '--entrypoint', '/homeatlas', image,
               '-addr', '0.0.0.0:8080', '-public-url', origin, '-data', '/data')
        created = True
        result['kernel_limits'] = {key:docker('exec',name,'cat',f'/sys/fs/cgroup/{key}') for key in ('memory.max','memory.swap.max','io.max')}
        assert int(result['kernel_limits']['memory.max'])==512*MIB
        assert int(result['kernel_limits']['memory.swap.max'])==0
        if io_device:
            assert f'rbps={read_bps}' in result['kernel_limits']['io.max'] and f'wbps={write_bps}' in result['kernel_limits']['io.max'], result['kernel_limits']
        for _ in range(100):
            try:
                if request('/api/health')[0]['status'] == 200:
                    break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(.1)
        else:
            raise RuntimeError('server did not start')
        code = docker('exec', name, 'cat', '/data/setup-code')
        setup, _ = request('/api/setup', json.dumps({'code':code,'name':'Load fixture','username':'load-fixture','password':PASSWORD,'householdName':'Disposable load fixture','timezone':'UTC'}).encode(), {'Content-Type':'application/json'})
        assert setup['status'] == 201, setup
        sampler = subprocess.Popen(['docker','exec','-i',name,'node','-e',SAMPLER.replace('setInterval(sample,50)', 'setInterval(sample,250)' if soak_seconds else 'setInterval(sample,50)')], stdin=subprocess.PIPE, stdout=sample_file, stderr=subprocess.PIPE)
        documents = []
        def upload_all():
            for index, (filename, data) in enumerate(corpus):
                boundary = 'HomeAtlasFixtureBoundary'
                metadata = json.dumps({'title':f'Document {index}: {filename}','category':'manuales','description':'Disposable load fixture','tags':['memory-test']}).encode()
                body = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n'.encode() + metadata +
                        f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode() + data + f'\r\n--{boundary}--\r\n'.encode())
                response, content = request('/api/documents', body, {'Content-Type':f'multipart/form-data; boundary={boundary}'})
                assert response['status'] == 201, (filename, response, content)
                documents.append({'id':json.loads(content)['id'],'sha256':hashlib.sha256(data).hexdigest(),'prefix_sha256':hashlib.sha256(data[:65536]).hexdigest(),'bytes':len(data)})
            return {'uploaded': len(documents)}
        phase('upload', upload_all)
        storage_clients = []
        def storage_action(view, start, count, seed, client=None, user=None, permission=None, expected=200):
            payload = {'start':str(start),'count':str(count),'bytes':'16384','seed':str(seed)}
            if user: payload['user']=user
            if permission: payload['permission']=permission
            body = json.dumps(payload).encode()
            response, content = request(f'/api/{storage_id}/actions/{view}',body,{'Content-Type':'application/json'},client=client)
            assert response['status']==expected and (expected!=200 or json.loads(content)['text']==f'{view} {count}'), (response,content)
            return response
        def storage_check_all():
            verified = 0
            for client, seed in storage_clients:
                for start in range(0,storage_records//2,32):
                    count = min(32,storage_records//2-start)
                    storage_action('check',start,count,seed,client)
                    verified += count
            if storage_shared:
                for client,first in ((opener,0),(storage_clients[1][0],1 if live_acl_restricted else 0)):
                    for start in range(first,1024,32):
                        count=min(32,1024-start)
                        storage_action('shared_check',start,count,3,client)
                        verified+=count
                if live_acl_restricted:
                    storage_action('shared_missing',0,1,3,storage_clients[1][0])
                    storage_action('shared_write',1,1,3,storage_clients[1][0],expected=403)
            return {'records_verified':verified,'bytes_verified':verified*16384}
        if storage_plugin:
            response, content = request('/api/household/invitations',json.dumps({'email':'storage-member@example.test','role':'member','permissions':[storage_id+'.read',storage_id+'.write']}).encode(),{'Content-Type':'application/json'})
            assert response['status']==201,(response,content)
            token = json.loads(content)['url'].split('#')[1]
            member_client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            response, content = request('/api/invitations/accept',json.dumps({'token':token,'name':'Storage fixture member','password':PASSWORD}).encode(),{'Content-Type':'application/json'},client=member_client)
            assert response['status']==201,(response,content)
            member_user = json.loads(content)['session']['user']['id']
            storage_clients = [(opener,1),(member_client,2)]
            def fill_storage():
                for client, seed in storage_clients:
                    for start in range(0,storage_records//2,32):
                        storage_action('write',start,min(32,storage_records//2-start),seed,client)
                if storage_shared:
                    for start in range(0,1024,32):
                        storage_action('shared_write',start,32,3)
                        storage_action('grant',start,32,3,user=member_user,permission='write')
                return {'records':storage_records,'value_bytes':storage_records*16384,'users':2,'shared_records':1024 if storage_shared else 0,'shared_value_bytes':1024*16384 if storage_shared else 0}
            result['persistent_storage'] = phase('storage-seed',fill_storage)
            phase('storage-check-before-copy',storage_check_all)
        phase('idle', lambda: time.sleep(1))
        def plugin_action():
            response, content = request('/api/ext-household-tools/actions/temperature', b'{"celsius":"25"}', {'Content-Type':'application/json'})
            assert response['status']==200 and json.loads(content)['text']=='25.00 °C equivalen a 77.00 °F.', (response,content)
            return response
        archive = Path(archive_dir.name) / f'{label}.zip.age'
        def backup(path):
            nonlocal archive_acl_restricted
            response, content = request('/api/household/backups', b'{}', {'Content-Type':'application/json','X-HomeAtlas-Backup-Version':'4','X-HomeAtlas-Backup-Password':base64.b64encode(PASSWORD.encode()).decode()}, path)
            if response['status'] != 200:
                assert response['status'] == 423 and json.loads(content)['error'] == 'household_busy', (response, content)
            else:
                with path.open('rb') as f:
                    header = f.read(1024)
                assert header.startswith(b'age-encryption.org/v1\n') and b' 18\n' in header
                if path==archive: archive_acl_restricted=live_acl_restricted
            return response
        for index in range(rounds):
            if tools_plugin:
                phase(f'plugin-before-copy-{index+1}', lambda: [plugin_action() for _ in range(10)])
            if storage_plugin:
                phase(f'storage-before-copy-{index+1}',lambda:[storage_action('check',0,min(32,storage_records//2),1) for _ in range(10)])
            values = phase(f'sequential-{index+1}', lambda: [backup(archive)])
            assert values[0]['status'] == 200, values
        for count in (2, 4):
            barrier = threading.Barrier(count)
            def concurrent_request(index):
                barrier.wait(timeout=10)
                return backup(Path(archive_dir.name) / f'{label}-concurrent-{count}-{index}.zip.age')
            def wave():
                with concurrent.futures.ThreadPoolExecutor(max_workers=count) as pool:
                    return list(pool.map(concurrent_request, range(count)))
            values = phase(f'concurrent-{count}', wave)
            assert sum(item['status']==200 for item in values)==1, values
        # A reader sent during a backup measures existing snapshot locking, not GC pauses.
        def reader_during_backup():
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                pending = pool.submit(backup, archive)
                time.sleep(.1)
                read, _ = request('/api/documents')
                copy = pending.result()
                assert read['status']==200 and copy['status']==200
                return {'backup':copy,'reader':read}
        phase('reader-during-backup', reader_during_backup)
        def restore():
            nonlocal live_acl_restricted
            if storage_plugin:
                # The restored state must undo real guest writes/deletes, not
                # merely reproduce already identical live records.
                storage_action('write',0,min(32,storage_records//2),99)
                storage_action('delete',0,min(32,storage_records//2),2,storage_clients[1][0])
            if storage_shared:
                storage_action('shared_write',2,32,99,storage_clients[1][0])
                storage_action('revoke',0,1,3,user=member_user)
                storage_action('grant',1,1,3,user=member_user,permission='read')
                live_acl_restricted=True
            # Host-side buffer is outside the server's cgroup.
            body = archive.read_bytes()
            cookie = '; '.join(f'{cookie.name}={cookie.value}' for handler in opener.handlers if isinstance(handler, urllib.request.HTTPCookieProcessor) for cookie in handler.cookiejar)
            headers = {'Origin':origin,'Cookie':cookie,'Content-Type':'application/octet-stream','X-HomeAtlas-Backup-Password':base64.b64encode(PASSWORD.encode()).decode(),'X-HomeAtlas-Confirm-Restore':'replace-household'}
            started = time.perf_counter()
            attempts = []
            while True:
                response, content = large_post(origin,'/api/household/restore',body,headers,timeout=request_timeout)
                attempts.append(response)
                if response['status'] != 423:
                    assert response['status']==200, (response, content)
                    return {**response,'duration_ms':(time.perf_counter()-started)*1000,'attempts':attempts}
                assert json.loads(content)['error']=='household_busy', content
                assert time.perf_counter()-started<30, ('restore admission remained busy',attempts)
                time.sleep(.1)
        phase('restore', restore)
        if storage_plugin:
            phase('storage-check-after-restore',storage_check_all)
        if tools_plugin:
            phase('plugin-after-restore', lambda: [plugin_action() for _ in range(10)])
        def verify():
            for document in documents:
                response, data = request(f'/api/documents/{document["id"]}/download')
                assert response['status']==200 and hashlib.sha256(data).hexdigest()==document['sha256']
            return {'sha256_verified':len(documents)}
        phase('verify-restored-originals', verify)
        if soak_seconds:
            cookie = '; '.join(f'{cookie.name}={cookie.value}' for handler in opener.handlers if isinstance(handler, urllib.request.HTTPCookieProcessor) for cookie in handler.cookiejar)
            def reader_worker(index):
                client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                iteration = 0
                while not stop_readers.is_set():
                    start_phase = active_phase
                    before = time.time()*1000
                    route = 'list' if iteration%2==0 else 'range'
                    try:
                        document = documents[(index+iteration)%len(documents)]
                        path = '/api/documents' if route=='list' else f'/api/documents/{document["id"]}/download'
                        headers = {'Cookie':cookie}
                        if route=='range':
                            headers['Range']='bytes=0-65535'
                        response, content = request(path, headers=headers, client=client)
                        assert response['status']==(200 if route=='list' else 206), response
                        if route=='range':
                            assert hashlib.sha256(content).hexdigest()==document['prefix_sha256'], 'reader content changed'
                        else:
                            assert isinstance(json.loads(content),list)
                        row = {'reader':index,'route':route,'start_epoch_ms':before,'end_epoch_ms':time.time()*1000,'started_during':start_phase,'finished_during':active_phase,**response}
                    except Exception as err:
                        row = {'reader':index,'route':route,'start_epoch_ms':before,'end_epoch_ms':time.time()*1000,'started_during':start_phase,'error':f'{type(err).__name__}: {err}'}
                        with reader_lock:
                            reader_errors.append(row)
                        stop_readers.set()
                    with reader_lock:
                        reader_file.write(json.dumps(row)+'\n')
                        reader_file.flush()
                        reader_counts[str(index)] = reader_counts.get(str(index),0)+1
                    iteration += 1
                    stop_readers.wait(.25)
            for index in range(readers):
                thread = threading.Thread(target=reader_worker,args=(index,),daemon=True)
                reader_threads.append(thread)
                thread.start()
            started = time.monotonic()
            result['soak'] = {'requested_seconds':soak_seconds,'readers':readers,'cycles':0,'cycle_idle_seconds':cycle_idle}
            while time.monotonic()-started < soak_seconds:
                index = result['soak']['cycles']+1
                if tools_plugin:
                    phase(f'plugin-before-soak-{index}', lambda: [plugin_action() for _ in range(10)])
                if storage_plugin:
                    phase(f'storage-before-soak-{index}',lambda:[storage_action('check',0,min(32,storage_records//2),1)])
                copies = phase(f'soak-{index}-backup',lambda:[backup(archive)])
                assert copies[0]['status']==200, copies
                phase(f'soak-{index}-restore',restore)
                if storage_plugin:
                    phase(f'storage-check-soak-{index}',storage_check_all)
                phase(f'soak-{index}-verify',verify)
                phase(f'soak-{index}-idle',lambda:time.sleep(cycle_idle))
                result['soak']['cycles']=index
                result['soak']['elapsed_seconds']=time.monotonic()-started
                assert not reader_errors, reader_errors[:3]
                print(f'{label}: soak {index} cycles, {time.monotonic()-started:.1f}s, readers={sum(reader_counts.values())}',flush=True)
            stop_readers.set()
            for thread in reader_threads:
                thread.join(timeout=140)
                assert not thread.is_alive(), 'reader did not finish'
            result['soak']['reader_requests']=sum(reader_counts.values())
            result['soak']['reader_errors']=reader_errors
            assert not reader_errors,reader_errors[:3]
        if recover_new:
            result['new_installation_recovery'] = recover_case(binary,output,label,image,storage_plugin,archive,documents,storage_records,io_options,storage_shared,archive_acl_restricted)
        phase('post-idle', lambda: time.sleep(1))
        result['memory_events'] = docker('exec',name,'cat','/sys/fs/cgroup/memory.events')
        result['cgroup_peak_bytes'] = int(docker('exec',name,'cat','/sys/fs/cgroup/memory.peak'))
        result['state'] = json.loads(docker('inspect','--format','{{json .State}}',name))
        assert result['state']['Running'] and not result['state']['OOMKilled']
        assert re.search(r'^oom_kill 0$',result['memory_events'],re.M)
        assert re.search(r'^oom 0$',result['memory_events'],re.M)
        result['completed'] = True
    finally:
        stop_readers.set()
        for thread in reader_threads:
            thread.join(timeout=140)
        reader_file.close()
        if sampler:
            try:
                sampler.stdin.close()
            except BrokenPipeError:
                pass
            sampler.wait(timeout=10)
            result['sampler_returncode'] = sampler.returncode
        sample_file.close()
        if created:
            # docker logs writes stderr to the parent's stderr unless explicitly captured.
            logs = subprocess.run(['docker','logs',name],text=True,capture_output=True)
            (output/f'{label}-server.log').write_text(logs.stdout+logs.stderr)
            result['final_state'] = json.loads(docker('inspect','--format','{{json .State}}',name))
            (output/f'{label}-result.json').write_text(json.dumps(result,indent=2)+'\n')
            docker('rm','-f',name)
        docker('volume','rm',volume)
        archive_dir.cleanup()
    samples = [json.loads(line) for line in sample_path.read_text().splitlines()]
    assert samples, 'no memory samples'
    assert result['sampler_returncode']==0
    assert any(g['forced'] for p in result['phases'] for g in p['gc_cycles']), 'forced GC traces missing'
    for entry in result['phases']:
        rows = [s for s in samples if entry['start_epoch_ms']<=s['epoch_ms']<=entry['end_epoch_ms']]
        entry['sampled_peak_rss_bytes'] = max((s['rss_bytes'] for s in rows), default=None)
        entry['sampled_peak_cgroup_bytes'] = max((s['cgroup_bytes'] for s in rows), default=None)
        entry['sampled_peak_anon_bytes'] = max((s['anon_bytes'] for s in rows), default=None)
        entry['sampled_peak_file_bytes'] = max((s['file_bytes'] for s in rows), default=None)
        entry['sampled_peak_socket_bytes'] = max((s['socket_bytes'] for s in rows), default=None)
        entry['gc_stw_ms'] = summarize([g['stw_ms'] for g in entry['gc_cycles']])
        entry['forced_gc_clock_ms'] = summarize([g['cycle_clock_ms'] for g in entry['gc_cycles'] if g['forced']])
    if soak_seconds:
        records=[json.loads(line) for line in (output/f'{label}-readers.jsonl').read_text().splitlines()]
        result['soak']['reader_summary']=reader_summary(records,result['phases'])
    (output/f'{label}-result.json').write_text(json.dumps(result,indent=2)+'\n')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True, help='Linux ARM64 production binary')
    parser.add_argument('--output', type=Path, required=True, help='new evidence directory')
    parser.add_argument('--image', default='node:24-bookworm-slim')
    parser.add_argument('--rounds', type=int, default=3)
    parser.add_argument('--volume-copies', type=int, nargs='+', default=[30], help='repetitions of the four-file corpus; 30 means 120 documents')
    parser.add_argument('--soak-seconds', type=int, default=0)
    parser.add_argument('--readers', type=int, default=0)
    parser.add_argument('--cycle-idle', type=float, default=2)
    parser.add_argument('--only-volume', action='store_true', help='skip mixed and near-limit cases for a focused soak')
    parser.add_argument('--io-device', help='daemon Linux block device, e.g. /dev/vda; never exposed inside container')
    parser.add_argument('--read-bps', type=int, default=0)
    parser.add_argument('--write-bps', type=int, default=0)
    parser.add_argument('--pdf', type=Path, help='optional real public PDF, replacing the generated guide')
    parser.add_argument('--household-tools-plugin', type=Path, help='optional packaged WASI example; exercise it before copies and after restore')
    parser.add_argument('--storage-plugin', type=Path, help='optional packaged ext-storage-load WASI fixture')
    parser.add_argument('--storage-shared', action='store_true', help='use ext-shared-load; add 1024 shared records and verify ACL restoration')
    parser.add_argument('--storage-records', type=int, default=4096, help='even total across two users, 16 KiB per record')
    parser.add_argument('--recover-new', action='store_true', help='recover and verify in a second empty 512 MiB installation; requires storage fixture')
    args = parser.parse_args()
    if args.output.exists() or args.rounds<1 or any(n<1 for n in args.volume_copies) or len(set(args.volume_copies))!=len(args.volume_copies):
        parser.error('use a new output directory and at least one round')
    if args.soak_seconds<0 or args.readers<0 or args.cycle_idle<0 or (args.readers and not args.soak_seconds):
        parser.error('readers require a positive soak duration; durations and counts must be nonnegative')
    if args.storage_records<2 or args.storage_records%2:
        parser.error('storage records must be a positive even total of at least two')
    if args.storage_shared and not args.storage_plugin:
        parser.error('shared storage requires the storage fixture package')
    if args.recover_new and not args.storage_plugin:
        parser.error('new-installation recovery requires the storage fixture package')
    if any((args.io_device,args.read_bps,args.write_bps)) and not (args.io_device and args.read_bps>0 and args.write_bps>0):
        parser.error('I/O device and positive read/write rates must be supplied together')
    binary = args.binary.resolve(strict=True)
    tools_plugin = args.household_tools_plugin.resolve(strict=True) if args.household_tools_plugin else None
    storage_plugin = args.storage_plugin.resolve(strict=True) if args.storage_plugin else None
    args.output.mkdir(parents=True)
    repo = Path(__file__).resolve().parent.parent
    guide = (args.pdf.name, args.pdf.read_bytes()) if args.pdf else ('guide.pdf', pdf())
    if not guide[1].startswith(b'%PDF-') or not 0<len(guide[1])<=20*MIB:
        parser.error('PDF must have a PDF header and be at most 20 MiB')
    fixtures = [guide,('house.jpg',(repo/'apps/web/src/assets/home-living-room.jpg').read_bytes()),
                ('maintenance.txt',('Home maintenance checklist\nCheck smoke alarms monthly.\n'*500).encode()),('scan.png',png(1024,1024))]
    near = ('large-scan.png',png(2640,2640))
    assert len(near[1])<20*MIB and len(near[1])>19*MIB
    report = {'schema':1,'completed':False,'limit_bytes':512*MIB,'swap_total_limit_bytes':512*MIB,
              'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),
              'environment':json.loads(docker('info','--format','{"kernel":{{json .KernelVersion}},"architecture":{{json .Architecture}},"server":{{json .ServerVersion}}}')),
              'image_id':docker('image','inspect',args.image,'--format','{{.Id}}'),
              'method':{'sample_ms':250 if args.soak_seconds else 50,'soak_seconds_per_case':args.soak_seconds,'readers':args.readers,'io_device':args.io_device,'read_bps':args.read_bps,'write_bps':args.write_bps,'gc':'GODEBUG=gctrace=1; no GOGC/GOMEMLIMIT overrides','client':'host Python, sampler Node inside measured cgroup','data':'disposable Docker disk volume; no personal data','clock':'host/container epoch milliseconds; small cross-VM clock skew possible','gc_clock':'GC cycle only; excludes scavenging after GC; not full cleanup duration','fixtures':'valid generated PDF/PNG/TXT and repository JPEG; PNG random RGB is incompressible stress data'},
              'fixtures':[{'name':n,'bytes':len(d),'sha256':hashlib.sha256(d).hexdigest()} for n,d in fixtures+[near]],'cases':[]}
    cases = [('mixed-12', fixtures*3)] + [(f'volume-{n*4}', fixtures*n) for n in args.volume_copies] + [('near-limit-4', [near]*4)]
    if args.only_volume:
        cases=[case for case in cases if case[0].startswith('volume-')]
    for label, corpus in cases:
        try:
            report['cases'].append(run_case(binary,args.output,label,corpus,args.image,args.rounds,soak_seconds=args.soak_seconds,readers=args.readers,io_device=args.io_device,read_bps=args.read_bps,write_bps=args.write_bps,cycle_idle=args.cycle_idle,tools_plugin=tools_plugin,storage_plugin=storage_plugin,storage_records=args.storage_records,recover_new=args.recover_new,storage_shared=args.storage_shared))
        except BaseException as err:
            report['failure']=f'{label}: {type(err).__name__}: {err}'
            raise
        finally:
            (args.output/'report.json').write_text(json.dumps(report,indent=2)+'\n')
    report['completed']=True
    (args.output/'report.json').write_text(json.dumps(report,indent=2)+'\n')
    print(f'Saved {args.output / "report.json"}',flush=True)


if __name__=='__main__':
    main()
