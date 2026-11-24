"""Make PostgreSQL's installed Mach-O files resolve libraries relative to themselves."""
import os
from pathlib import Path
import subprocess
import sys


def output(*args):
    return subprocess.check_output(args, text=True)


root = Path(sys.argv[1]).resolve(strict=True)
prefix = Path(sys.argv[2])
for directory in ('bin', 'lib'):
    for binary in sorted((root / directory).rglob('*')):
        if not binary.is_file():
            continue
        kind = output('file', '-b', binary)
        if 'Mach-O' not in kind:
            continue
        if 'arm64' not in kind:
            raise SystemExit(f'Unexpected executable architecture: {binary.name}')

        edits = []
        dependency_paths = [line.strip().split(' (', 1)[0] for line in output('otool', '-L', binary).splitlines()[1:]]
        if 'shared library' in kind:
            library_id = output('otool', '-D', binary).splitlines()[1]
            edits.extend(('-id', '@loader_path/' + binary.name))
            dependency_paths.remove(library_id)
        for dependency in dependency_paths:
            if dependency.startswith(('/usr/lib/', '/System/Library/')):
                continue
            if not dependency.startswith(str(prefix) + '/'):
                raise SystemExit(f'Unexpected library dependency in {binary.name}: {dependency}')
            target = root / Path(dependency).relative_to(prefix)
            if not target.is_file():
                raise SystemExit(f'Bundled library is missing: {target.name}')
            edits.extend(('-change', dependency, '@loader_path/' + os.path.relpath(target, binary.parent)))
        if edits:
            subprocess.run(['install_name_tool', *edits, binary], check=True)
            subprocess.run(['codesign', '--force', '--sign', '-', binary], check=True)
