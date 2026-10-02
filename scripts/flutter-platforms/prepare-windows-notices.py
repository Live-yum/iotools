"""Include unchanged official MSVC recipient terms and traceable CRT provenance.

Reading/copying these terms accepts no new agreement. Runtime recipient terms
are not themselves a grant of distributor rights; the linked Visual Studio
agreement and Microsoft REDIST list govern the applicable redistribution rights.
"""
from pathlib import Path
import hashlib
import json
import os
import subprocess
import sys
import urllib.request

root = Path(__file__).resolve().parents[2]
bundle = root / 'mobile/build/windows/x64/runner/Release'
out = bundle / 'licenses/Microsoft-Visual-C-Runtime'
out.mkdir(parents=True, exist_ok=True)
url = 'https://visualstudio.microsoft.com/wp-content/uploads/2021/09/Visual-C-Runtime-2015-2022-License-1.docx'
expected = 'f1e3d56ceb2ad68aae0711b910375009e651ac5530fa0760f0dea6e81e54fae1'
with urllib.request.urlopen(url, timeout=60) as response:
    data = response.read(1024*1024)
assert hashlib.sha256(data).hexdigest() == expected, 'Official runtime terms changed; review before packaging'
(out / 'Visual-C-Runtime-2015-2022-License-1.docx').write_bytes(data)
redist_url = 'https://learn.microsoft.com/en-us/visualstudio/releases/2022/redistribution'
with urllib.request.urlopen(redist_url, timeout=60) as response:
    redist = response.read(1024*1024)
assert b'Visual C++ Runtime Files' in redist and b'VC\\redist' in redist, 'Unexpected Microsoft REDIST document'
(out / 'Visual-Studio-2022-REDIST-official.html').write_bytes(redist)
crt = Path(sys.argv[1])
records = []
for name in ('msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll'):
    source, target = crt / name, bundle / name
    assert source.is_file() and target.is_file()
    digest = hashlib.sha256(target.read_bytes()).hexdigest()
    assert hashlib.sha256(source.read_bytes()).hexdigest() == digest
    environment = os.environ.copy()
    environment['IOTOOLS_RUNTIME_INSPECT'] = str(target)
    info = subprocess.check_output(['pwsh', '-NoProfile', '-Command',
        '$p=$env:IOTOOLS_RUNTIME_INSPECT; $f=Get-Item -LiteralPath $p; '
        '$s=Get-AuthenticodeSignature -LiteralPath $p; '
        '@{version=$f.VersionInfo.FileVersion;signature_status=[string]$s.Status;'
        'signer=$s.SignerCertificate.Subject}|ConvertTo-Json -Compress'],
        env=environment, text=True)
    signature = json.loads(info)
    assert signature['signature_status'] == 'Valid' and 'Microsoft' in signature['signer'], signature
    records.append({'file': name, 'sha256': digest, 'installed_source': str(source), **signature})
record = {'source_sha': os.environ['IOTOOLS_SHA'], 'runtime_terms': {'url': url, 'sha256': expected},
    'official_license_page': 'https://visualstudio.microsoft.com/license-terms/vs2022-cruntime/',
    'official_redistribution_list': {'url': redist_url, 'sha256': hashlib.sha256(redist).hexdigest()},
    'applicable_visual_studio_agreement': 'https://visualstudio.microsoft.com/license-terms/vs2022-ga-proenterprise/',
    'runtime_files': records,
    'scope': 'Original release CRT files from the runner-installed Visual Studio redist directory. '
             'Recipient terms are included unchanged. Redistribution remains subject to the applicable '
             'licensed Visual Studio agreement and Microsoft REDIST list; this record is not legal certification.'}
(out / 'provenance.json').write_text(json.dumps(record, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
(out / '说明.txt').write_text('随包 Microsoft Visual C++ 运行库的原始接收者条款和 DLL 来源、版本、哈希见本目录。\n'
    '条款文件来自微软官方网站，未修改。运行库再分发仍受适用的 Visual Studio 许可协议和官方 REDIST 清单约束；'
    '接收者条款本身不是再分发授权证明。链接保存在 provenance.json。\n', encoding='utf-8')
print('PASS unchanged official MSVC terms and installed CRT provenance')
