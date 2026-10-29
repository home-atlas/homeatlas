import importlib.util
import http.server
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import threading
import unittest
import zlib

spec = importlib.util.spec_from_file_location('backup_load', Path(__file__).with_name('measure-backup-load.py'))
load = importlib.util.module_from_spec(spec)
spec.loader.exec_module(load)


class LoadMeasurementTests(unittest.TestCase):
    def test_early_busy_response_survives_large_upload_rejection(self):
        class Busy(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                body = b'{"error":"household_busy"}'
                self.send_response(423)
                self.send_header('Content-Length',str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                self.close_connection = True

            def log_message(self, *args):
                pass

        server = http.server.HTTPServer(('127.0.0.1',0),Busy)
        thread = threading.Thread(target=server.handle_request,daemon=True)
        thread.start()
        try:
            response, content = load.large_post(f'http://127.0.0.1:{server.server_port}',
                                                '/restore',b'x'*(8<<20),{})
            self.assertEqual(response['status'],423)
            self.assertEqual(content,b'{"error":"household_busy"}')
        finally:
            thread.join(timeout=2)
            server.server_close()

    def test_readers_overlapping_restore_are_reported_separately(self):
        phases = [{'name':'soak-1-restore','start_epoch_ms':20,'end_epoch_ms':100}]
        rows = [
            {'start_epoch_ms':1,'end_epoch_ms':3,'duration_ms':2,'started_during':'soak-1-backup'},
            {'start_epoch_ms':19,'end_epoch_ms':101,'duration_ms':82,'started_during':'soak-1-backup'},
            {'start_epoch_ms':105,'end_epoch_ms':108,'duration_ms':3,'started_during':'soak-1-idle'},
            {'error':'timeout'},
        ]
        summary = load.reader_summary(rows, phases)
        self.assertEqual(summary['latency_ms']['export_without_restore']['max'],2)
        self.assertEqual(summary['latency_ms']['overlapping_restore']['max'],82)
        self.assertEqual(summary['latency_ms']['all']['count'],3)
        self.assertEqual(len(summary['errors']),1)

    def test_invalid_soak_limits_stop_before_starting_docker(self):
        with tempfile.TemporaryDirectory() as directory:
            base = [sys.executable, str(Path(__file__).with_name('measure-backup-load.py')),
                    '--binary','/nonexistent-homeatlas-binary','--output',str(Path(directory)/'new')]
            for flags in (['--readers','2'], ['--soak-seconds','-1'], ['--read-bps','100'], ['--io-device','/dev/vda'], ['--storage-records','3'], ['--recover-new'], ['--storage-shared']):
                result = subprocess.run(base+flags,capture_output=True,text=True)
                self.assertEqual(result.returncode,2,result.stderr)

    def test_gc_clock_and_cpu_are_distinct_from_stw(self):
        line = 'gc 3 @1.2s 0%: 0.03+1.2+0.02 ms clock, 0.3+0/0.2/0.1+0.2 ms cpu, 256->256->1 MB, 512 MB goal (forced)'
        cycle, = load.gc_cycles(line)
        self.assertTrue(cycle['forced'])
        self.assertAlmostEqual(cycle['stw_ms'], .05)
        self.assertAlmostEqual(cycle['cycle_clock_ms'], 1.25)
        self.assertAlmostEqual(cycle['cpu_ms'], .8)
        self.assertEqual(load.gc_cycles('not a GC trace'), [])

    def test_generated_png_has_valid_chunks_and_pixels(self):
        image = load.png(8, 8)
        self.assertEqual(image[:8], b'\x89PNG\r\n\x1a\n')
        pos, compressed = 8, b''
        while pos < len(image):
            size, = struct.unpack('>I', image[pos:pos+4])
            kind, data = image[pos+4:pos+8], image[pos+8:pos+8+size]
            crc, = struct.unpack('>I', image[pos+8+size:pos+12+size])
            self.assertEqual(zlib.crc32(kind+data), crc)
            if kind == b'IDAT':
                compressed += data
            pos += size+12
        self.assertEqual(len(zlib.decompress(compressed)), 8*(1+8*3))

    def test_generated_pdf_xref_points_to_objects(self):
        pdf = load.pdf()
        start = int(pdf.split(b'startxref\n')[1].splitlines()[0])
        lines = pdf[start:].splitlines()
        self.assertEqual(lines[:2], [b'xref', b'0 6'])
        for index, line in enumerate(lines[3:8], 1):
            offset = int(line[:10])
            self.assertTrue(pdf[offset:].startswith(f'{index} 0 obj\n'.encode()))


if __name__ == '__main__':
    unittest.main()
