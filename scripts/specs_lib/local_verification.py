"""Invocation-local evidence, using a constrained native Make verification plan.

No result can be loaded from disk. Make still computes dependencies; unfamiliar
recipes fail closed instead of disappearing into a parallel verification list.
"""
import argparse
from contextlib import contextmanager, ExitStack
from dataclasses import dataclass
import hashlib
import io
import json
import math
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import time
import tempfile

import specs
import user_flows as uf
from specs_lib.processes import LONG_SUITE_COMMANDS, reconcile
from specs_lib.input_watch import InputWatch, resolution_chain
from specs_lib.isolation import managed

PREPARATION = {'npm-install', 'frontend', 'embed', 'build', 'fake-provider', 'packaging', 'browser-optimize', 'directory'}
MUTABLE = ('verification-results', 'web/test-results', 'web/playwright-report',
           'web/performance-results', 'web/benchmark-results',
           'docs/specs/reconciliations', 'docs/user-flows/reconciliations')
GENERATED = ('bin', 'web/node_modules', 'internal/web/dist', 'internal/pluginbundle/dist', 'dist')
MAKE_CARRIERS = ('MAKEFLAGS', 'MFLAGS', 'MAKEFILES', 'GNUMAKEFLAGS', 'MAKEOVERRIDES')
CGO_DEFAULTS = {'CGO_CPPFLAGS': [], 'CGO_CFLAGS': ['-O2', '-g'],
                'CGO_CXXFLAGS': ['-O2', '-g'], 'CGO_FFLAGS': ['-O2', '-g'],
                'CGO_LDFLAGS': ['-O2', '-g']}
GCC_CARRIERS = ('CPATH', 'C_INCLUDE_PATH', 'CPLUS_INCLUDE_PATH', 'OBJC_INCLUDE_PATH',
                'LIBRARY_PATH', 'COMPILER_PATH', 'GCC_EXEC_PREFIX')
LOADER_CARRIERS = ('LD_PRELOAD', 'LD_AUDIT', 'LD_LIBRARY_PATH')
# Reviewed native ownership plumbing from main. Planning omits only this exact
# prologue under the already authenticated managed() owner; all selected native
# prerequisites and recipes still come from the inspected current Makefile.
MAKE_OWNER_PROLOGUE = 'e2e524d325c5b0d9f2c35788ad7dccb40ff1684d3123544849966a67e38800c8'
MAKE_OWNER_RECIPES = {
    '7286dbcb2cf606f203c91fcb7efe6acc51ec626778909ca0906c191f28226e0e',
    'acc6ceab8d0bb3a146a9230ab6bc11a14e2d2acbc19177c10530b21e2ae21f7f',
    'b0852362f56309620a7822156211006e21281dbfa20f2f247ca588d9d02439c2',
}
# Browser inventory supports these reviewed repository configurations only.
BROWSER_CONFIGS = {'playwright.config.ts': 'b46558bd33acba71ddaa0613cfb2b0900f1e68fca79d5759a77e671b5001533c', 'playwright.performance.config.ts': 'ba2327cb04366db9a365adca7f0ddb4dab72af540dbf148b97861cc5df07760a'}
NPM_INTERPRETER = '/usr/bin/env'
NPM_HELPERS = ('python3', 'node', 'go', 'sh', 'git', 'gcc', 'g++', 'cc', 'c++', 'as', 'ld', 'setpriv')


@dataclass(frozen=True)
class Step:
    phase: str
    cwd: Path
    command: tuple


def command_timeout(root, step, requested=None):
    """Bound maintained long suites while preserving shorter caller deadlines."""
    limit = 900 if step.cwd == root and step.command in LONG_SUITE_COMMANDS else 600
    if requested is not None:
        if (isinstance(requested, bool) or not isinstance(requested, (int, float)) or
                not math.isfinite(requested) or requested <= 0):
            raise uf.FlowError('Local verification timeout must be positive and finite')
        limit = min(limit, requested)
    return limit


def excluded(name):
    return any(name == p or name.startswith(p + '/') for p in MUTABLE)


def owned_runtime(root):
    """Only the authenticated enclosing owner's exact private namespace is output."""
    selected = os.environ.get('CODE_FACTORY_VERIFY_RUN')
    if selected and os.environ.get('CODE_FACTORY_VERIFY_ROOT') == str(root.resolve()):
        run = Path(selected).resolve()
        if run.is_relative_to(root.resolve()):
            return run.relative_to(root.resolve()).as_posix()
    return None


def runtime_output(name, private=None):
    parts = Path(name).parts
    return ('.git' in parts or helper_output(name) or excluded(name) or
            (private is not None and (name == private or name.startswith(private + '/'))) or
            name == 'web/node_modules/.vite-temp' or name.startswith('web/node_modules/.vite-temp/'))


def source_output(name, private=None):
    return runtime_output(name, private) or any(name == p or name.startswith(p + '/') for p in GENERATED)


@contextmanager
def planning_source(root, review_path=None):
    """Observe source and review lookups before consumption and native planning."""
    private = owned_runtime(root)
    watch = InputWatch(root, lambda name: source_output(name, private))
    try:
        if review_path is not None:
            watch.external(review_path)
        # Native plans are inputs too. Observe the selected planner and every
        # earlier lookup candidate before Make can supply any captured output.
        for directory in sorted(executable_search_inputs()):
            watch.external(directory / 'make')
        planner = shutil.which('make')
        if not planner:
            raise uf.FlowError('Verification executable unavailable: make')
        watch.external(Path(planner))
        yield watch.check
    finally:
        original_error = sys.exception()
        cleanup_error = None
        for action in (watch.check, watch.close):
            try:
                action()
            except BaseException as error:
                cleanup_error = cleanup_error or error
        if cleanup_error is not None and original_error is None:
            raise cleanup_error


def validate_summary(root, review_path, summary):
    if not summary:
        return
    selected = summary.resolve()
    if selected == review_path.resolve() or (selected.is_relative_to(root) and
            not selected.is_relative_to(root / 'verification-results')):
        raise uf.FlowError('Summary must be separate from review decisions and repository inputs/receipts')


def digest_rows(rows):
    digest = hashlib.sha256()
    for row in rows:
        for field in row:
            value = field if isinstance(field, bytes) else str(field).encode()
            digest.update(len(value).to_bytes(8, 'big'))
            digest.update(value)
    return digest.hexdigest()


def source_identity(root):
    files = source_snapshot(root)
    return digest_rows((name, files.modes[name], files[name])
                       for name in sorted(files) if not excluded(name))


def file_stamp(value):
    return (value.st_dev, value.st_ino, value.st_mode, value.st_size,
            value.st_mtime_ns, value.st_ctime_ns)


def artifact_digest(path, expected):
    """Hash the admitted regular object without blocking on a substituted FIFO."""
    return read_regular(path, expected, lambda source: hashlib.file_digest(source, hashlib.sha256).digest())


def read_regular(path, expected, consume):
    """Consume one stable regular descriptor, including during input discovery."""
    descriptor = os.open(path, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        before = os.fstat(descriptor)
        if not stat.S_ISREG(before.st_mode) or file_stamp(before) != file_stamp(expected):
            raise uf.FlowError('Prepared input changed while opening: ' + str(path))
        with os.fdopen(descriptor, 'rb', closefd=False) as source:
            result = consume(source)
        if (file_stamp(os.fstat(descriptor)) != file_stamp(before) or
                file_stamp(path.lstat()) != file_stamp(before)):
            raise uf.FlowError('Prepared input changed during traversal: ' + str(path))
        return result
    finally:
        original_error = sys.exception()
        try:
            os.close(descriptor)
        except BaseException:
            if original_error is None:
                raise


def input_content(path):
    """Read admitted alias-bound bytes without blocking on a substituted FIFO."""
    edges, resolved = resolution_chain(path)
    content = read_regular(resolved, resolved.lstat(), lambda source: source.read())
    if resolution_chain(path) != (edges, resolved):
        raise uf.FlowError('Prepared input lookup changed while reading')
    return content


def review_content(path):
    return input_content(path)


def input_text(path):
    """Retain native text decoding and universal newlines after typed admission."""
    with io.TextIOWrapper(io.BytesIO(input_content(path))) as source:
        return source.read()


def source_snapshot(root, ref=None):
    """Keep ordinary Git blobs, admitting worktree inputs through typed opens."""
    def read(path):
        return read_regular(path, path.lstat(), lambda source: source.read())
    return uf.snapshot(root, ref, read_file=read)


def artifact_identity(root):
    """Include ignored helpers, dependencies and config; omit only known outputs.

    Symlinks within dependencies are inputs along with their targets. External
    targets and special files are unsupported rather than incompletely hashed.
    """
    rows, private = [], owned_runtime(root)
    for directory, directories, names in os.walk(root, followlinks=False):
        parent = Path(directory)
        directories[:] = sorted(d for d in directories if not runtime_output((parent / d).relative_to(root).as_posix(), private))
        for name in sorted(names + [d for d in directories if (parent / d).is_symlink()]):
            path = parent / name
            relative = path.relative_to(root).as_posix()
            if runtime_output(relative, private):
                continue
            before = path.lstat()
            mode = before.st_mode
            if stat.S_ISLNK(mode):
                edges, resolved = resolution_chain(path)
                entered = False
                for alias_parent, alias_name in edges:
                    lookup = alias_parent / alias_name
                    if lookup.is_relative_to(root):
                        entered = True
                        if runtime_output(lookup.relative_to(root).as_posix(), private):
                            raise uf.FlowError('Excluded input symlink lookups are unsupported: ' + relative)
                    elif entered and not root.is_relative_to(lookup):
                        raise uf.FlowError('External input symlinks are unsupported: ' + relative)
                if (not resolved.is_relative_to(root) or not resolved.exists() or
                        runtime_output(resolved.relative_to(root).as_posix(), private)):
                    raise uf.FlowError('Excluded or external input symlinks are unsupported: ' + relative)
                target = os.readlink(path)
                if file_stamp(path.lstat()) != file_stamp(before) or resolution_chain(path) != (edges, resolved):
                    raise uf.FlowError('Prepared input symlink changed during traversal: ' + relative)
                rows.append((relative, mode, target))
            elif stat.S_ISREG(mode):
                rows.append((relative, mode, artifact_digest(path, before)))
            else:
                raise uf.FlowError('Special input files are unsupported: ' + relative)
    return digest_rows(sorted(rows))


def inspect_make(source, target):
    """Accept the repository's simple dependency graph, before Make can run it."""
    if any(os.environ.get(k) for k in MAKE_CARRIERS):
        raise uf.FlowError('Local verification requires no inherited Make overrides')
    prefix = source.split('.PHONY:', 1)[0]
    if hashlib.sha256(prefix.encode()).hexdigest() == MAKE_OWNER_PROLOGUE:
        source = source[len(prefix):]
    # These unselected recursive entry recipes contain only the pinned native
    # parent-PID query. They never execute in this plan; selected recursion still
    # fails the graph walk below. Unknown expansions remain rejected globally.
    lines = []
    for line in source.splitlines(keepends=True):
        text = line.rstrip('\r\n')
        if hashlib.sha256(text.encode()).hexdigest() in MAKE_OWNER_RECIPES:
            line = line.replace('$(shell echo $$PPID)', '0')
        lines.append(line)
    source = ''.join(lines)
    if 'MAKEFILE_LIST' in source or re.search(r'\$[({]\s*(shell|file|eval|call|guile)\b', source):
        raise uf.FlowError('Executable Make expansions are unsupported by local verification')
    graph, recipes, current = {}, {}, []
    logical = source.replace('\\\n', ' ')
    for line in logical.splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        if line.startswith('\t'):
            for name in current:
                recipes.setdefault(name, []).append(line.strip())
            continue
        current = []
        if re.match(r'\s*(-?include|sinclude|define|ifdef|ifndef|ifeq|ifneq|else|endif|export\s+SHELL)\b', line):
            raise uf.FlowError('Supplemental or conditional Make inputs are unsupported')
        assignment = re.match(r'(?:(?:override|export)\s+)*([A-Za-z0-9_.-]+)\s*(?::=|\?=|\+=|=)', line)
        if assignment:
            name = assignment[1]
            fixture_export = line.strip() == 'override export CODE_FACTORY_REAL_CODEX := 0'
            if (('export' in line[:assignment.end()].split() and not fixture_export) or
                    name in ('SHELL', '.SHELLFLAGS', '.RECIPEPREFIX', 'PATH', *MAKE_CARRIERS, 'MAKELEVEL') or
                    (name in os.environ and name != 'CODE_FACTORY_REAL_CODEX')):
                raise uf.FlowError('Custom Make environment or shell assignments are unsupported')
            continue
        match = re.fullmatch(r'([^:]+):\s*([^:;=]*)', line)
        if not match:
            raise uf.FlowError('Unsupported Make declaration: ' + line)
        names, dependencies = match[1].split(), match[2].split()
        if any(not re.fullmatch(r'[A-Za-z0-9_.-]+', n) for n in names + dependencies):
            raise uf.FlowError('Dynamic Make dependencies are unsupported')
        if 'Makefile' in names:
            raise uf.FlowError('Makefile remaking is unsupported')
        if any(n.startswith('.') and n not in ('.PHONY', '.NOTPARALLEL') for n in names):
            raise uf.FlowError('Special Make execution modes are unsupported')
        if all(n in ('.PHONY', '.NOTPARALLEL') for n in names):
            continue
        for name in names:
            graph.setdefault(name, []).extend(dependencies)
        current = names
    visited = set()
    def visit(name):
        if name in visited:
            return
        visited.add(name)
        if name not in graph:
            raise uf.FlowError('Unknown verification dependency: ' + name)
        for recipe in recipes.get(name, []):
            if recipe.lstrip('@-').startswith('+') or '$(MAKE)' in recipe or '${MAKE}' in recipe:
                raise uf.FlowError('Recursive or forced recipes are unsupported in the local plan')
        for dependency in graph[name]:
            visit(dependency)
    visit(target)
    return source


def native_plan(root, target, base):
    source = input_text(root / 'Makefile')
    planned = inspect_make(source, target)
    # Make reads exactly the bytes inspected above. A concurrent writer cannot
    # replace the live file with an executable expansion between preflight/read.
    with tempfile.TemporaryDirectory(prefix='local-verification-plan-') as directory:
        selected = Path(directory) / 'Makefile'
        selected.write_text(planned)
        result = subprocess.run(['make', '-rR', '--dry-run', '--no-print-directory', '--no-keep-going',
                                 '-f', str(selected), target, 'FLOW_ARGS=--base ' + base], cwd=root,
                                env={**os.environ, **uf.FIXTURE_ENV}, text=True, capture_output=True, timeout=15)
    if result.returncode:
        raise uf.FlowError('Native Make could not produce the local verification plan')
    if input_text(root / 'Makefile') != source:
        raise uf.FlowError('Makefile changed while planning verification')
    steps = []
    for line in result.stdout.splitlines():
        shell_text = line.removeprefix('cd web && ').replace("'test_specs*.py'", 'test_specs.py')
        if re.search(r'[;$`|&<>*?\[\]{}#]', shell_text):
            raise uf.FlowError('Shell expansion is unsupported in the local plan: ' + line)
        words = shlex.split(line)
        cwd = root
        if words[:3] == ['cd', 'web', '&&']:
            words, cwd = words[3:], root / 'web'
        wrapper = '../scripts/verification_timing.py' if cwd != root else 'scripts/verification_timing.py'
        if words == ['python3', wrapper, 'worker-start']:
            continue
        if words == ['mkdir', '-p', 'bin'] and cwd == root:
            steps.append(Step('directory', cwd, tuple(words)))
            continue
        if words[:3] != ['python3', wrapper, 'command'] or len(words) < 6 or words[4] != '--':
            raise uf.FlowError('Unrecognized verification recipe: ' + line)
        from verification_timing import PHASES
        if words[3] not in PHASES or any(w in ('&&', ';', '|', '>', '>>', '<', '&') for w in words[5:]):
            raise uf.FlowError('Unsupported verification recipe: ' + line)
        steps.append(Step(words[3], cwd, tuple(words[5:])))
    if not steps:
        raise uf.FlowError('Empty native verification plan')
    return steps


def expand(root, command, base):
    if command[0] != 'make':
        return [Step('required-suite', root, tuple(command))]
    if len(command) != 2 or command[1] not in ('build', 'test-performance'):
        raise uf.FlowError('Unsupported required Make command; no checks were omitted')
    return native_plan(root, command[1], base)


def validate_review(affected, reviews):
    if not isinstance(reviews, dict) or set(reviews) != set(affected):
        raise uf.FlowError('Local verification requires review decisions for every affected subject')
    errors = [error for subject, item in affected.items()
              for error in specs.constraints(subject, item, reviews[subject])]
    if errors:
        raise uf.FlowError('\n'.join(errors))


class InvocationCancellation:
    def __init__(self):
        self.number = None

    def interrupt(self, number, frame):
        if self.number is None:
            self.number = number

    def check(self):
        if self.number == signal.SIGINT:
            raise KeyboardInterrupt
        if self.number is not None:
            raise SystemExit(128 + self.number)


@contextmanager
def invocation_signals(cancellation=None, *, check_pending=True):
    """Record outer cancellation; checkpoints unwind through complete cleanup."""
    previous, cancellation = {}, cancellation or InvocationCancellation()
    try:
        for number in (signal.SIGINT, signal.SIGTERM):
            previous[number] = signal.signal(number, cancellation.interrupt)
        yield cancellation
    finally:
        original_error = sys.exception()
        for number, handler in previous.items():
            signal.signal(number, handler)
        if check_pending and not isinstance(original_error, (KeyboardInterrupt, SystemExit)):
            cancellation.check()


def npm_working_directories(root):
    return tuple(cwd for cwd in (root, root / 'web') if cwd.is_dir())


def npm_script_prefixes(cwd, installation):
    """Mirror one maintained npm run-script context's executable search prefixes."""
    return (*(parent / 'node_modules/.bin' for parent in (cwd, *cwd.parents)),
            installation / 'node_modules/@npmcli/run-script/lib/node-gyp-bin')


def npm_script_directories(root, installation):
    """Watch the same prefixes used to select launchers in each npm context."""
    result = []
    for cwd in npm_working_directories(root):
        result.extend(npm_script_prefixes(cwd, installation))
    return tuple(dict.fromkeys(result))


def npm_inputs(root):
    """Support ordinary npm config; reject custom script execution contexts."""
    files = set()
    for cwd in npm_working_directories(root):
        for key in ('userconfig', 'globalconfig', 'script-shell', 'node-options'):
            result = subprocess.run(['npm', 'config', 'get', key], cwd=cwd,
                                    capture_output=True, text=True, check=True, timeout=15)
            value = result.stdout.strip()
            if key == 'script-shell':
                if value != 'null':
                    raise uf.FlowError('Custom npm script-shell is unsupported by local verification')
            elif key == 'node-options':
                if value not in ('null', ''):
                    raise uf.FlowError('Custom npm node-options startup inputs are unsupported by local verification')
            elif not value or not Path(value).is_absolute():
                raise uf.FlowError('Selected npm configuration has an unsupported location')
            else:
                files.add(Path(value))
    shell = Path('/bin/sh')
    if not shell.is_file():
        raise uf.FlowError('Default npm script shell is unavailable')
    files.add(shell)
    launcher = shutil.which('npm')
    if not launcher:
        raise uf.FlowError('Verification executable unavailable: npm')
    installation = Path(launcher).resolve(strict=True).parents[1]
    for cwd in npm_working_directories(root):
        directories = npm_script_prefixes(cwd, installation)
        search = os.pathsep.join([*(str(path) for path in directories), os.environ['PATH']])
        for name in NPM_HELPERS:
            selected, canonical = shutil.which(name, path=search), shutil.which(name)
            if not selected and name in ('python3', 'node', 'go'):
                raise uf.FlowError('Maintained npm helper is unavailable: ' + name)
            if selected is None and canonical is None:
                continue
            # Resource discovery uses the inherited PATH. Npm's augmented search
            # may retain aliases to those tools, but cannot select another launcher.
            if (selected is None or canonical is None or
                    Path(selected).resolve(strict=True) != Path(canonical).resolve(strict=True)):
                raise uf.FlowError('Npm helper selection differs from inventoried PATH: ' + name)
            files.add(selected_input(selected))
            if name == 'python3':
                # Python can select a virtual environment beside an executable
                # alias even when that alias resolves to the canonical binary.
                for executable in (Path(selected), Path(canonical)):
                    files.update((executable.parent / 'pyvenv.cfg', executable.parent.parent / 'pyvenv.cfg'))
                if (selected != canonical and
                        python_inputs(root, selected) != python_inputs(root, canonical)):
                    raise uf.FlowError('Npm Python import selection differs from inventoried PATH')
    return files


def browser_inputs(root):
    """Use the installed, pinned Playwright registry for the standard Chromium graph."""
    if os.environ.get('PLAYWRIGHT_BROWSERS_PATH') == '0':
        raise uf.FlowError('In-package Playwright browser inputs are unsupported')
    for name, expected in BROWSER_CONFIGS.items():
        config = root / 'web' / name
        if not config.is_file() or hashlib.sha256(input_content(config)).hexdigest() != expected:
            raise uf.FlowError('Custom Playwright browser selection or config is unsupported')
    # This internal registry is pinned by package-lock; unknown layouts fail closed.
    script = ("const {registry:{registry}}=require('playwright-core/lib/coreBundle');"
              "const names=['chromium','chromium-headless-shell','ffmpeg'];"
              "console.log(JSON.stringify(names.map(name=>registry.findExecutable(name).executablePath())));")
    try:
        result = subprocess.run(['node', '-e', script], cwd=root / 'web',
                                capture_output=True, text=True, check=True, timeout=15)
        paths = uf.decode_json(result.stdout)
        if not isinstance(paths, list) or len(paths) != 3 or any(
                not isinstance(p, str) or not Path(p).is_absolute() or not Path(p).is_file() for p in paths):
            raise ValueError('incomplete browser inventory')
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise uf.FlowError('Selected Playwright Chromium inventory is unavailable; install locked dependencies and browsers') from error
    # Chromium executables have one intervening directory; FFmpeg has none.
    return {Path(p).parent.parent for p in paths[:2]} | {Path(paths[2]).parent}


def selected_input(value):
    """Preserve the admitted spelling; content traversal resolves it later."""
    if not isinstance(value, str) or not Path(value).is_absolute():
        raise uf.FlowError('Selected external input requires an absolute path')
    return Path(value)


def validate_python_environment():
    """Require import selectors whose meaning is independent of helper cwd."""
    for name in ('PYTHONPATH', 'PYTHONHOME', 'PYTHONUSERBASE'):
        selected = os.environ.get(name, '')
        values = [selected] if name == 'PYTHONUSERBASE' else selected.split(os.pathsep)
        if selected and any(not value or not Path(value).is_absolute() for value in values):
            raise uf.FlowError('Relative or empty ' + name + ' entries are unsupported by local verification')


def python_inputs(root, executable):
    """Inventory admitted import roots/data; reject selected excluded sources."""
    validate_python_environment()
    result = subprocess.run([executable, '-c', 'import json,sys; print(json.dumps(sys.path))'],
                            cwd=root, capture_output=True, text=True, check=True, timeout=15)
    paths = uf.decode_json(result.stdout)
    if not isinstance(paths, list) or any(not isinstance(path, str) for path in paths):
        raise uf.FlowError('Selected Python import inventory is unavailable')
    directories, files = set(), set()
    for value in paths:
        path = Path(value or root)
        if not path.is_absolute():
            path = root / path
        edges, resolved = resolution_chain(path)
        for selected in [path, resolved, *(parent / name for parent, name in edges)]:
            if ('__pycache__' in selected.parts or
                    (selected.is_relative_to(root) and
                     runtime_output(selected.relative_to(root).as_posix(), owned_runtime(root)))):
                raise uf.FlowError('Selected Python import search intersects excluded runtime or cache inputs')
        if path.is_relative_to(root):
            continue
        (directories if path.is_dir() else files).add(path)
    return directories, files


def git_inputs(root):
    """Bind the file-backed Git inputs used by Go's status and log queries."""
    selectors = ('GIT_DIR', 'GIT_COMMON_DIR', 'GIT_WORK_TREE', 'GIT_IMPLICIT_WORK_TREE',
        'GIT_INDEX_FILE', 'GIT_OBJECT_DIRECTORY', 'GIT_ALTERNATE_OBJECT_DIRECTORIES',
        'GIT_GRAFT_FILE', 'GIT_SHALLOW_FILE', 'GIT_REPLACE_REF_BASE', 'GIT_NAMESPACE',
        'GIT_CONFIG', 'GIT_CONFIG_PARAMETERS', 'GIT_CONFIG_COUNT', 'GIT_CONFIG_SYSTEM',
        'GIT_CONFIG_GLOBAL', 'GIT_CONFIG_NOSYSTEM')
    if any(os.environ.get(name) for name in selectors):
        raise uf.FlowError('Custom Git input selection is unsupported by local verification')
    def query(*arguments, input=None):
        return subprocess.run(['git', *arguments], cwd=root, capture_output=True,
                              input=input, text=True, check=True, timeout=15).stdout
    locations = query('rev-parse', '--path-format=absolute', '--git-dir', '--git-common-dir').splitlines()
    if len(locations) != 2 or any(not Path(path).is_absolute() for path in locations):
        raise uf.FlowError('Selected Git metadata inventory is unavailable')
    gitdir, common = map(Path, locations)
    directories, files = set(), {gitdir / 'HEAD', gitdir / 'commondir',
        gitdir / 'index', gitdir / 'config.worktree', gitdir / 'info/sparse-checkout',
        common / 'HEAD', common / 'config', common / 'packed-refs', common / 'shallow',
        common / 'info/exclude', common / 'info/attributes', common / 'info/grafts',
        common / 'objects/info/alternates'}
    if not (root / '.git').is_dir():
        files.add(root / '.git')
    for name in ('refs', 'objects'):
        path = common / name
        (directories if path.is_dir() else files).add(path)
    # Linked worktrees keep refs/worktree, refs/bisect and refs/rewritten here.
    # An absent private refs directory is an input too: later creation matters.
    path = gitdir / 'refs'
    (directories if path.is_dir() else files).add(path)
    for path in (common / 'info/grafts', common / 'objects/info/alternates'):
        if path.exists() and input_content(path).strip():
            raise uf.FlowError('Git grafts and alternate object stores are unsupported')
    for variable in ('GIT_CONFIG_SYSTEM', 'GIT_CONFIG_GLOBAL', 'GIT_ATTR_SYSTEM'):
        paths = query('var', variable).splitlines()
        if not paths or any(not Path(path).is_absolute() for path in paths):
            raise uf.FlowError('Selected Git configuration paths are unavailable')
        files.update(map(Path, paths))
    config_home = Path(os.environ.get('XDG_CONFIG_HOME') or Path.home() / '.config')
    if not config_home.is_absolute():
        raise uf.FlowError('Relative Git configuration homes are unsupported')
    files.update(config_home / 'git' / name for name in ('ignore', 'attributes'))
    rows = query('config', '--null', '--show-origin', '--list').split('\0')
    if rows[-1] or len(rows) % 2 != 1:
        raise uf.FlowError('Selected Git configuration inventory is unavailable')
    for origin, item in zip(rows[0:-1:2], rows[1:-1:2]):
        key, _, value = item.partition('\n')
        key = key.lower()
        if re.fullmatch(r'filter\.(?:set|unset|unspecified)\.(?:clean|process)', key):
            raise uf.FlowError('Reserved Git filter driver names are unsupported by local verification')
        if (not origin.startswith('file:') or
                key == 'include.path' or key.startswith('includeif.') or
                key in ('core.fsmonitor', 'core.excludesfile', 'core.attributesfile') or
                (key == 'extensions.refstorage' and value != 'files')):
            raise uf.FlowError('Custom Git configuration inputs are unsupported')
        path = Path(origin[5:])
        files.add(path if path.is_absolute() else root / path)
    tracked = query('ls-files', '--cached', '-z')
    attributes = query('check-attr', '-z', '--stdin', 'filter', input=tracked).split('\0')
    if attributes[-1] or len(attributes) % 3 != 1:
        raise uf.FlowError('Selected Git filter inventory is unavailable')
    if any(name != 'filter' or value not in ('unspecified', 'unset')
           for name, value in zip(attributes[1:-1:3], attributes[2:-1:3])):
        raise uf.FlowError('Selected Git clean/process filters are unsupported by local verification')
    return directories, files


@contextmanager
def git_readonly():
    """Keep status semantically unchanged without optional index refresh writes."""
    previous = os.environ.get('GIT_OPTIONAL_LOCKS')
    os.environ['GIT_OPTIONAL_LOCKS'] = '0'
    try:
        yield
    finally:
        if previous is None:
            os.environ.pop('GIT_OPTIONAL_LOCKS', None)
        else:
            os.environ['GIT_OPTIONAL_LOCKS'] = previous


def go_module_inputs(root):
    """Cover the selected module build list before generated embeds are prepared."""
    unavailable = 'Selected Go module inventory is unavailable; install inputs with go mod download all before review'
    try:
        result = subprocess.run(['go', 'list', '-m', '-mod=readonly', '-f',
                                 '{{.Dir}}\t{{.GoMod}}', 'all'],
                                cwd=root, capture_output=True, text=True, check=True, timeout=30,
                                env={**os.environ, 'GOPROXY': 'off'})
    except subprocess.SubprocessError as error:
        raise uf.FlowError(unavailable) from error
    directories, files = set(), set()
    for row in set(result.stdout.splitlines()) - {''}:
        fields = row.split('\t')
        if len(fields) != 2 or not all(fields):
            raise uf.FlowError(unavailable)
        directory, manifest = map(selected_input, fields)
        if not directory.is_dir() or not manifest.is_file():
            raise uf.FlowError('Selected Go module inputs are unavailable')
        if not directory.is_relative_to(root):
            directories.add(directory)
        if not manifest.is_relative_to(root):
            files.add(manifest)
    return directories, files


def gcc_include_inputs(root, compiler, language):
    """Inventory the concrete GCC search list, including absent lookup candidates."""
    result = subprocess.run([str(compiler), '-E', '-Wp,-v', '-x', language, '-'],
                            input='', cwd=root, env={**os.environ, 'LC_ALL': 'C'},
                            capture_output=True, text=True, check=True, timeout=15)
    lines = result.stderr.splitlines()
    markers = ['#include "..." search starts here:', '#include <...> search starts here:', 'End of search list.']
    if len(lines) > 128 or any(lines.count(marker) != 1 for marker in markers):
        raise uf.FlowError('Selected GCC header inventory is unsupported')
    first, angle, end = (lines.index(marker) for marker in markers)
    if not first < angle < end or end != len(lines) - 1:
        raise uf.FlowError('Selected GCC header inventory is unsupported')
    directories, absent = set(), set()
    for line in lines[:first]:
        ignored = re.fullmatch(r'ignoring (nonexistent|duplicate) directory "([^"]+)"', line)
        if ignored is None:
            raise uf.FlowError('Selected GCC header inventory is unsupported')
        path = selected_input(ignored[2])
        if ignored[1] == 'nonexistent':
            if path.exists() or path.is_symlink():
                raise uf.FlowError('Selected GCC header lookup changed during discovery')
            absent.add(path)
        elif path.is_dir():
            directories.add(path)
        else:
            raise uf.FlowError('Selected GCC duplicate header directory is unavailable')
    for line in [*lines[first + 1:angle], *lines[angle + 1:end]]:
        if not line.startswith(' '):
            raise uf.FlowError('Selected GCC header inventory is unsupported')
        path = selected_input(line.strip())
        if not path.is_dir():
            raise uf.FlowError('Selected GCC header directory is unavailable')
        directories.add(path)
    if not directories:
        raise uf.FlowError('Selected GCC header inventory is unavailable')
    return directories, absent


def validate_loader_environment():
    """Reject unsupported ELF and Bash startup selectors before spawning helpers."""
    if (any(value for name, value in os.environ.items() if name.startswith('LD_')) or
            os.environ.get('GLIBC_TUNABLES')):
        raise uf.FlowError('Custom ELF loader inputs are unsupported by local verification')
    if os.environ.get('BASH_ENV'):
        raise uf.FlowError('Custom Bash startup inputs are unsupported by local verification')


def executable_search_inputs():
    """Admit absolute PATH contexts; watch all lookup directories and their aliases."""
    if any(name != 'PATH' and name.lower() == 'path' and value for name, value in os.environ.items()):
        raise uf.FlowError('Noncanonical npm PATH environment selectors are unsupported')
    paths = os.environ.get('PATH', '').split(os.pathsep)
    if any(not path or not Path(path).is_absolute() for path in paths):
        raise uf.FlowError('Relative or empty PATH lookup contexts are unsupported')
    return {Path(path) for path in paths}



# Supported system ABI for the maintained Linux verification helpers.
SYSTEM_LOADER = Path('/lib64/ld-linux-x86-64.so.2')
SYSTEM_LIBRARIES = Path('/usr/lib/x86_64-linux-gnu')
LIBRARY_LOOKUPS = tuple(map(Path, ('/lib/x86_64-linux-gnu', '/usr/lib/x86_64-linux-gnu', '/lib', '/usr/lib', '/lib64', '/usr/lib64')))


def native_library_inputs(directories, files, executables):
    """Inventory the supported glibc ABI; reject unrepresented ELF search contexts."""
    if os.uname().machine != 'x86_64' or not SYSTEM_LOADER.is_file() or not SYSTEM_LIBRARIES.is_dir():
        raise uf.FlowError('Unsupported native library ABI for local verification')
    if any(value for name, value in os.environ.items() if name.startswith('LD_')) or os.environ.get('GLIBC_TUNABLES'):
        raise uf.FlowError('Custom native library loader contexts are unsupported')
    preload = Path('/etc/ld.so.preload')
    if preload.exists() and input_text(preload).strip():
        raise uf.FlowError('System ELF preloads are unsupported by local verification')
    ldconfig, readelf = shutil.which('ldconfig'), shutil.which('readelf')
    if not ldconfig or not readelf:
        raise uf.FlowError('Native library inventory tools are unavailable')
    selected = {Path(ldconfig), Path(readelf), SYSTEM_LOADER, Path('/etc/ld.so.cache'),
                Path('/etc/ld.so.conf'), Path('/etc/ld.so.conf.d'), preload}
    configuration = Path('/etc/ld.so.conf.d')
    if configuration.is_dir():
        selected.update(path for path in configuration.iterdir() if path.is_file())
    result = subprocess.run([ldconfig, '-p'], capture_output=True, text=True, check=True, timeout=15,
                            env={**os.environ, 'LC_ALL': 'C'})
    lookups = set(LIBRARY_LOOKUPS)
    for line in result.stdout.splitlines()[1:]:
        if '=> ' not in line:
            if line.strip() and not line.startswith('Cache generated by: ldconfig '):
                raise uf.FlowError('Unsupported native library cache inventory')
            continue
        if 'x86-64' not in line:
            continue
        path = selected_input(line.split('=> ', 1)[1].strip())
        if not path.is_file():
            raise uf.FlowError('Selected native library is unavailable')
        selected.add(path)
        lookups.add(path.parent)
        lookups.add(path.resolve(strict=True).parent)
    for directory in lookups:
        selected.add(directory)
        if directory.is_dir():
            selected.update(path for path in directory.iterdir() if path.is_file() or path.is_symlink() and not path.is_dir())
            hwcaps = directory / 'glibc-hwcaps'
            selected.add(hwcaps)
            if hwcaps.exists():
                directories.add(hwcaps)
    directories.add(SYSTEM_LIBRARIES)
    # Native admission must see the same nested directory aliases whose contents
    # enter later identity checks and mutation monitoring.
    directories.update(helper_roots(directories))
    primary = {Path(shutil.which(name)) for name in executables}
    primary.update(path for path in files if path.is_file())
    for directory in directories:
        for path in directory.rglob('*'):
            if path.is_file() and ('.so' in path.name or path.suffix == '.node' or path.name in
                    ('chrome', 'headless_shell', 'chrome-headless-shell', 'ffmpeg-linux',
                     'chrome_crashpad_handler', 'chrome_sandbox')):
                primary.add(path)
    primary.update(path for path in selected if path.is_file())
    elf = []
    for path in sorted(primary):
        resolved = path.resolve(strict=True)
        if read_regular(resolved, resolved.lstat(), lambda stream: stream.read(4)) == b'\x7fELF':
            elf.append(path)
    full_roots = {directory.resolve(strict=True) for directory in directories}
    direct_roots = {directory.resolve(strict=False) for directory in lookups}
    for start in range(0, len(elf), 128):
        result = subprocess.run([readelf, '-l', '-d', *map(str, elf[start:start + 128])],
                                capture_output=True, text=True, check=True, timeout=15,
                                env={**os.environ, 'LC_ALL': 'C'})
        current = elf[start] if len(elf[start:start + 128]) == 1 else None
        for line in result.stdout.splitlines():
            if line.startswith('File: '):
                current = Path(line[6:])
            if 'Requesting program interpreter:' in line:
                interpreter = line.split('Requesting program interpreter:', 1)[1].strip().rstrip(']')
                if interpreter != str(SYSTEM_LOADER):
                    raise uf.FlowError('Alternate ELF program interpreters are unsupported')
            if any(tag in line for tag in ('(AUDIT)', '(DEPAUDIT)', '(FILTER)', '(AUXILIARY)')):
                raise uf.FlowError('Custom ELF library indirection is unsupported')
            if '(NEEDED)' in line and '/' in line.split('[', 1)[1].rsplit(']', 1)[0]:
                raise uf.FlowError('Path-qualified ELF dependencies are unsupported')
            if '(RPATH)' in line or '(RUNPATH)' in line:
                value = line.split('[', 1)[1].rsplit(']', 1)[0]
                for entry in value.split(':') if value else ():
                    for origin in sorted({current.parent, current.resolve(strict=True).parent}):
                        expanded = re.sub(r'\$(?:\{ORIGIN\}|ORIGIN(?=/|$))', lambda match: str(origin), entry)
                        path = Path(expanded)
                        if not expanded or '$' in expanded or not path.is_absolute():
                            raise uf.FlowError('Relative or tokenized ELF library searches are unsupported')
                        resolved = path.resolve(strict=False)
                        if not resolved.exists():
                            selected.add(path)
                            continue
                        if resolved not in direct_roots and not any(resolved.is_relative_to(parent) for parent in full_roots):
                            raise uf.FlowError('ELF library search lies outside inventoried inputs: ' + str(current) + ': ' + expanded)
    return selected


def helper_inputs(root, executables):
    """Inventory selected npm, Go and GCC subprocess resources beyond launchers."""
    validate_loader_environment()
    if os.environ.get('ESBUILD_BINARY_PATH'):
        raise uf.FlowError('Custom ESBUILD_BINARY_PATH helpers are unsupported by local verification')
    if os.environ.get('NODE_OPTIONS'):
        raise uf.FlowError('Custom NODE_OPTIONS startup inputs are unsupported by local verification')
    directories, files = set(), set()
    for executable in executables:
        if Path(executable).name.startswith('python'):
            selected_directories, selected_files = python_inputs(root, executable)
            directories.update(selected_directories)
            files.update(selected_files)
    if 'npm' in executables:
        launcher = shutil.which('npm')
        if not launcher:
            raise uf.FlowError('Verification executable unavailable: npm')
        files.add(selected_input(launcher))
        installation = Path(launcher).resolve(strict=True).parents[1]
        manifest = uf.decode_json(input_text(installation / 'package.json'))
        if manifest.get('name') != 'npm':
            raise uf.FlowError('Selected npm installation has an unsupported helper inventory')
        directories.add(installation)
        files.update(npm_inputs(root))
        directories.update(browser_inputs(root))
    if 'go' not in executables:
        files.update(native_library_inputs(directories, files, executables))
        return directories, files
    if any(os.environ.get(name) for name in GCC_CARRIERS):
        raise uf.FlowError('Custom GCC include, library or helper search contexts are unsupported')
    cache_program = subprocess.run(['go', 'env', '-json', 'GOCACHEPROG'], cwd=root,
                                   capture_output=True, text=True, check=True, timeout=15)
    if uf.decode_json(cache_program.stdout).get('GOCACHEPROG'):
        raise uf.FlowError('Custom effective GOCACHEPROG build-cache helpers are unsupported')
    result = subprocess.run(['go', 'env', '-json', 'GOROOT', 'GOTOOLDIR', 'CC', 'CXX', 'GOENV', 'GOFLAGS', 'GOWORK', *CGO_DEFAULTS],
                            cwd=root, capture_output=True, text=True, check=True, timeout=15)
    settings = uf.decode_json(result.stdout)
    if settings.get('GOFLAGS') or settings.get('GOWORK') not in (None, '', 'off'):
        raise uf.FlowError('Custom Go flags, overlays and workspaces are unsupported')
    if any(not isinstance(settings.get(name), str) or shlex.split(settings[name]) != default
           for name, default in CGO_DEFAULTS.items()):
        raise uf.FlowError('Custom effective cgo compiler flags are unsupported by local verification')
    for key in ('GOROOT', 'GOTOOLDIR'):
        path = selected_input(settings[key])
        if not path.is_dir():
            raise uf.FlowError('Selected Go helper directory is unavailable')
        directories.add(path)
    if settings['GOENV'] != 'off':
        files.add(selected_input(settings['GOENV']))
    for key in ('CC', 'CXX'):
        command = shlex.split(settings[key])
        if len(command) != 1 or not shutil.which(command[0]):
            raise uf.FlowError('Selected C compiler requires an unsupported helper inventory')
        compiler = selected_input(shutil.which(command[0]))
        files.add(compiler)
        for helper in ('cc1', 'cc1plus', 'collect2', 'lto1', 'lto-wrapper', 'as', 'ld'):
            result = subprocess.run([str(compiler), '-print-prog-name=' + helper], cwd=root,
                                    capture_output=True, text=True, check=True, timeout=15)
            name = result.stdout.strip()
            selected = shutil.which(name) if name and not Path(name).is_absolute() else name
            if not selected or not Path(selected).is_file():
                raise uf.FlowError('Selected compiler helper inventory is incomplete')
            path = selected_input(selected)
            files.add(path)
            if helper in ('cc1', 'cc1plus', 'collect2', 'lto1', 'lto-wrapper'):
                directories.add(path.parent)
        result = subprocess.run([str(compiler), '-print-libgcc-file-name'], cwd=root,
                                capture_output=True, text=True, check=True, timeout=15)
        library = selected_input(result.stdout.strip())
        if not library.is_file():
            raise uf.FlowError('Selected compiler resource inventory is incomplete')
        files.add(library)
        directories.add(library.parent)
        headers, absent = gcc_include_inputs(root, compiler, 'c' if key == 'CC' else 'c++')
        directories.update(headers)
        files.update(absent)
    module_directories, module_files = go_module_inputs(root)
    directories.update(module_directories)
    files.update(module_files)
    directories = {path for path in directories if not any(
        path != other and path.is_relative_to(other) for other in directories)}
    files.update(native_library_inputs(directories, files, executables))
    return directories, files


def helper_identity(directories, files, *, omit=None):
    omit = helper_output if omit is None else omit
    rows = []
    contents, lookups = {}, {}
    def identity(value):
        return (value.st_dev, value.st_ino, value.st_mode, value.st_size,
                value.st_mtime_ns, value.st_ctime_ns)
    selected = set(files)
    def incomplete(error):
        raise uf.FlowError('External helper traversal lost input coverage') from error
    for root in sorted(directories):
        edges, resolved_root = resolution_chain(root)
        rows.extend((str(parent / name), os.readlink(parent / name)) for parent, name in edges
                    if (parent / name).is_symlink())
        if not root.is_dir():
            raise uf.FlowError('External helper directory is unavailable')
        for directory, children, names in os.walk(resolved_root, followlinks=False, onerror=incomplete):
            parent = Path(directory)
            children[:] = [name for name in children if not omit(name)]
            rows.append((str(parent), 'directory', parent.stat().st_mode))
            for name in children:
                if (parent / name).is_symlink() and parent / name not in directories:
                    raise uf.FlowError('Symlinked external helper directories are unsupported')
            selected.update(parent / name for name in names if not omit(parent / name))
    for path in sorted(selected):
        if not path.exists() and not path.is_symlink():
            rows.append((str(path), 'absent'))
            continue
        if path.is_symlink() and not path.exists():
            edges, resolved = resolution_chain(path)
            rows.extend((str(parent / name), os.readlink(parent / name)) for parent, name in edges
                        if (parent / name).is_symlink())
            rows.append((str(path), str(resolved), 'absent-target'))
            continue
        resolved = path.resolve(strict=True)
        if resolved.is_dir():
            rows.append((str(path), str(resolved), 'lookup-directory', path.lstat().st_mode, resolved.stat().st_mode))
            continue
        if not resolved.is_file():
            raise uf.FlowError('External helper input is not a regular file')
        # Walked regular files have a resolved, symlink-free parent tree; its
        # root chain is already recorded. Explicit files can have other aliases.
        if path in files or path.is_symlink():
            edges, _ = resolution_chain(path)
            rows.extend((str(parent / name), os.readlink(parent / name)) for parent, name in edges
                        if (parent / name).is_symlink())
        alias_mode = path.lstat().st_mode
        expected = resolved.stat()
        descriptor = os.open(resolved, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK)
        try:
            with os.fdopen(descriptor, 'rb', closefd=False) as source:
                before = os.fstat(descriptor)
                if not stat.S_ISREG(before.st_mode):
                    raise uf.FlowError('External helper input is not a regular file')
                stamp = identity(before)
                if identity(expected) != stamp:
                    raise uf.FlowError('External helper input changed while opening')
                cached = contents.get(stamp[:2])
                if ((cached is not None and cached[0] != stamp) or
                        (resolved in lookups and lookups[resolved] != stamp)):
                    raise uf.FlowError('External helper input changed during traversal')
                digest = cached[1] if cached is not None else hashlib.file_digest(source, hashlib.sha256).digest()
                if identity(os.fstat(descriptor)) != stamp or identity(resolved.stat()) != stamp:
                    raise uf.FlowError('External helper input changed during traversal')
                contents[stamp[:2]] = (stamp, digest)
                lookups[resolved] = stamp
                rows.append((str(path), str(resolved), alias_mode, before.st_mode, digest))
        finally:
            original_error = sys.exception()
            try:
                os.close(descriptor)
            except BaseException:
                if original_error is None:
                    raise
    return digest_rows(rows)


def helper_output(name):
    # The prefix bypasses tagged automatic caches, but __pycache__ can also be
    # an importable namespace containing real source or legacy bytecode modules.
    parts = Path(name).parts
    return (bool(os.environ.get('PYTHONPYCACHEPREFIX')) and '__pycache__' in parts and
            re.fullmatch(r'.+\.cpython-\d+(?:\.opt-\d+)?\.pyc(?:\.\d+)?', parts[-1]) is not None)


def helper_roots(directories, *, omit=None):
    """Retain directory aliases and separately monitor their resolved contents."""
    omit = helper_output if omit is None else omit
    result, pending, visited = set(directories), list(directories), set()
    def incomplete(error):
        raise uf.FlowError('External helper traversal lost input coverage') from error
    while pending:
        root = pending.pop()
        resolved = root.resolve(strict=True)
        if resolved in visited:
            continue
        visited.add(resolved)
        for directory, children, _ in os.walk(resolved, followlinks=False, onerror=incomplete):
            parent = Path(directory)
            children[:] = [name for name in children if not omit(name)]
            for name in children:
                path = parent / name
                if path.is_symlink():
                    if not path.is_dir():
                        raise uf.FlowError('External helper directory alias is unavailable')
                    if path not in result:
                        result.add(path)
                        pending.append(path)
    return result


class GoCache:
    """Keep one bound private directory, empty before each required command."""
    def __init__(self, root):
        self.root = Path(root)
        before = self.root.lstat()
        self.identity = (before.st_dev, before.st_ino, before.st_mode)
        # Cache files are temporary command outputs. No later required command
        # consumes them; the directory's own lookup and identity remain guarded.
        self.watch = InputWatch(self.root, lambda name: True)
        try:
            self.watch.external(self.root, contents=False)
            self.check()
        except BaseException:
            try:
                self.watch.close()
            except BaseException:
                pass
            raise

    def check(self):
        if os.environ.get('GOCACHE') != str(self.root):
            raise uf.FlowError('Go build cache selection changed')
        self.watch.check()
        value = self.root.lstat()
        if (value.st_dev, value.st_ino, value.st_mode) != self.identity:
            raise uf.FlowError('Go build cache directory binding changed')
        self.watch.check()

    def reset(self):
        self.check()
        for path in self.root.iterdir():
            if path.is_dir() and not path.is_symlink():
                shutil.rmtree(path)
            else:
                path.unlink()
        self.check()
        if any(self.root.iterdir()):
            raise uf.FlowError('Go build cache could not start empty')

    def close(self):
        try:
            self.check()
        finally:
            self.watch.close()


@contextmanager
def go_build_cache():
    """Never consume a caller's retained compilation archives."""
    temporary = tempfile.TemporaryDirectory(prefix='go-build-cache-', dir=os.environ['TMPDIR'])
    previous = os.environ.get('GOCACHE')
    try:
        os.environ['GOCACHE'] = temporary.name
        cache = GoCache(temporary.name)
        try:
            yield cache
        finally:
            original_error = sys.exception()
            try:
                cache.close()
            except BaseException:
                if original_error is None:
                    raise
    finally:
        if previous is None:
            os.environ.pop('GOCACHE', None)
        else:
            os.environ['GOCACHE'] = previous
        original_error = sys.exception()
        try:
            temporary.cleanup()
        except BaseException:
            if original_error is None:
                raise


class Session:
    def __init__(self, root, base, steps, *, runner=None, timeout=None, go_cache=None):
        self.root, self.base = root, base
        self.go_cache = go_cache
        steps = tuple(steps)
        self.timeout = timeout
        for step in steps:
            command_timeout(root, step, timeout)
        self.head = uf.revision(root, 'HEAD')
        self.environment = dict(os.environ)
        self.search_directories = executable_search_inputs()
        executables = {step.command[0] for step in steps} | {'make', 'git', 'bash', 'node'}
        if 'npm' in executables:
            # Maintained npm fixtures enter Python ownership and compile the Go host.
            executables.update((NPM_INTERPRETER, 'python3', 'go'))
        self.executables = sorted(executables)
        if 'go' in self.executables:
            # The maintained Go provider fixtures execute this PATH-selected helper.
            self.executables.append('setpriv')
            self.executables.sort()
        if 'npm' in self.executables:
            launcher = shutil.which('npm')
            if not launcher:
                raise uf.FlowError('Verification executable unavailable: npm')
            installation = Path(launcher).resolve(strict=True).parents[1]
            # Checkout prefixes become artifact inputs after npm preparation.
            # External prefixes must already be stable during preparation.
            self.search_directories.update(path for path in npm_script_directories(root, installation)
                                           if not path.is_relative_to(root))
        if any('/' in name and not Path(name).is_absolute() for name in self.executables):
            raise uf.FlowError('Relative verification executable paths are unsupported')
        self.runner = runner or uf.run_verification
        self.completed, self.rows = {}, []
        self.source = source_identity(root)
        selected_helpers = helper_inputs(root, self.executables)
        selected_git = git_inputs(root)
        self.helper_directories = helper_roots(selected_helpers[0])
        self.helper_files = set(selected_helpers[1])
        self.git_directories = helper_roots(selected_git[0], omit=lambda name: False)
        self.git_files = set(selected_git[1])
        self.helper_watches = []
        self.prepared = None
        self.guard = lambda: None
        self.versions = self.tool_versions()
        self.private = owned_runtime(root)
        self.watch = InputWatch(root, lambda name: source_output(name, self.private))
        self.artifact_watch = None
        try:
            self.watch.external(root / '.git', contents=False)
            lookup_names = {Path(name).name for name in self.executables}
            lookup_names.update((*NPM_HELPERS, 'npx'))
            lookup_names.update(path.name for path in self.helper_files)
            for directory in sorted(self.search_directories):
                for name in sorted(lookup_names):
                    self.watch.external(directory / name)
            monitored = [(path, helper_output, self.helper_files) for path in sorted(self.helper_directories)]
            monitored.extend((path, lambda name: False, self.git_files) for path in sorted(self.git_directories))
            for directory, omit, selected_files in monitored:
                self.watch.external(directory, contents=False)
                self.helper_watches.append(InputWatch(directory.resolve(strict=True), omit))
                selected_files.update(path for path in directory.rglob('*')
                                      if path.is_symlink() and not path.is_dir())
            for name in self.executables:
                self.watch.external(Path(shutil.which(name)))
            for path in self.helper_files | self.git_files:
                self.watch.external(path)
            # Bind discovery to selectors observed under these installed watches.
            # A selection completed before installation must not seed a stale plan.
            selected_now = helper_inputs(root, self.executables)
            git_now = git_inputs(root)
            if (selected_now != selected_helpers or git_now != selected_git or
                    helper_roots(selected_now[0]) != self.helper_directories or
                    helper_roots(git_now[0], omit=lambda name: False) != self.git_directories):
                raise uf.FlowError('External helper selection changed during discovery')
            self.tools = self.tool_identity()
            self.assert_current()
        except BaseException:
            try:
                self.close()
            except BaseException:
                pass
            raise

    def tool_versions(self):
        versions = []
        flags = {'go': 'version', 'node': '--version', 'npm': '--version',
                 'python3': '--version', 'make': '--version', 'git': '--version'}
        for name in self.executables:
            flag = flags.get(Path(name).name)
            if flag:
                result = subprocess.run([name, flag], capture_output=True, timeout=15, check=True)
                versions.append((name, result.stdout, result.stderr))
        return digest_rows(versions)

    def tool_identity(self):
        rows = []
        for name in self.executables:
            executable = shutil.which(name)
            if not executable:
                raise uf.FlowError('Verification executable unavailable: ' + name)
            path = Path(executable).resolve(strict=True)
            before = path.lstat()
            rows.append((name, str(path), before.st_mode, artifact_digest(path, before)))
        rows.append(('subprocess-helpers', helper_identity(self.helper_directories, self.helper_files)))
        rows.append(('git-vcs-inputs', helper_identity(self.git_directories, self.git_files, omit=lambda name: False)))
        return digest_rows(rows)

    def check_watches(self):
        error = None
        if self.go_cache is not None:
            try:
                self.go_cache.check()
            except BaseException as failure:
                error = failure
        for watch in [self.watch, *self.helper_watches, self.artifact_watch]:
            if watch is not None:
                try:
                    watch.check()
                except BaseException as failure:
                    error = error or failure
        if error is not None:
            raise error

    def assert_current(self):
        self.guard()
        self.check_watches()
        if (self.environment != dict(os.environ) or self.head != uf.revision(self.root, 'HEAD') or
                self.source != source_identity(self.root) or self.tools != self.tool_identity()):
            raise uf.FlowError('Verification source, comparison, environment or toolchain changed')
        if self.prepared is not None and self.prepared != artifact_identity(self.root):
            raise uf.FlowError('Prepared verification inputs changed; run fresh verification')
        # Digest traversal can overlap a restored write; consume its events too.
        self.check_watches()

    def execute(self, step):
        self.assert_current()
        key = (step.cwd, step.command)
        if key in self.completed:
            self.guard()
            return self.completed[key]
        if self.go_cache is not None:
            self.go_cache.reset()
        self.guard()
        started = time.monotonic()
        print('Verify local: ' + json.dumps(list(step.command)), flush=True)
        row = {'phase': step.phase, 'seconds': None, 'exit_code': None}
        self.rows.append(row)
        try:
            result = self.runner(list(step.command), cwd=step.cwd,
                                 timeout=command_timeout(self.root, step, self.timeout))
            row['exit_code'] = result.returncode
        finally:
            row['seconds'] = round(time.monotonic() - started, 3)
        if result.returncode:
            raise uf.FlowError('Required local verification failed; no receipts retained')
        self.assert_current()
        self.completed[key] = row
        return row

    def freeze(self):
        self.assert_current()
        if self.artifact_watch:
            raise uf.FlowError('Prepared verification boundary already selected')
        self.artifact_watch = InputWatch(self.root, lambda name: runtime_output(name, self.private))
        self.prepared = artifact_identity(self.root)
        self.assert_current()

    def close(self):
        error = None
        for watch in [self.watch, *self.helper_watches, self.artifact_watch]:
            if watch is not None:
                try:
                    if watch.fd >= 0 and not watch.observed_failure:
                        watch.check()
                except BaseException as failure:
                    error = error or failure
                try:
                    watch.close()
                except BaseException as failure:
                    error = error or failure
        if error is not None:
            raise error


def prepare_once(session, steps):
    """Keep first occurrences inside one environment/ownership-bound session."""
    context = (session.root, session.base, tuple(sorted(session.environment.items())))
    seen = set()
    for step in steps:
        if step.phase not in PREPARATION:
            continue
        if session.environment != dict(os.environ):
            session.assert_current()
        key = (context, step.cwd, step.command)
        if key not in seen:
            session.execute(step)
            seen.add(key)


class Publications:
    """Keep exact published objects available for rollback after owner release.

    CLI receipts and summaries replace paths atomically. A later owner therefore
    publishes another inode, which these descriptors can never truncate/unlink.
    """
    def __init__(self):
        self.descriptors = []

    def capture(self, path):
        descriptor = os.open(path, os.O_RDWR | os.O_CLOEXEC | os.O_NOFOLLOW)
        try:
            if not stat.S_ISREG(os.fstat(descriptor).st_mode):
                raise uf.FlowError('Published verification evidence is not a regular file')
            self.descriptors.append(descriptor)
        except BaseException:
            os.close(descriptor)
            raise

    def invalidate(self):
        error = None
        for descriptor in self.descriptors:
            try:
                os.ftruncate(descriptor, 0)
            except BaseException as failure:
                error = error or failure
        if error is not None:
            raise error

    def close(self):
        error = None
        for descriptor in self.descriptors:
            try:
                os.close(descriptor)
            except BaseException as failure:
                error = error or failure
        self.descriptors.clear()
        if error is not None:
            raise error


class PythonCache:
    """Fresh source-derived bytecode is a watched generated invocation input."""
    def __init__(self, directory):
        self.directory = Path(directory)
        self.descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
        try:
            self.identity = os.fstat(self.descriptor)
        except BaseException:
            os.close(self.descriptor)
            raise
        self.watch = None
        self.digest = None
        self.prefix = f'/proc/{os.getpid()}/fd/{self.descriptor}'

    def bound(self, *, named=True):
        try:
            current = os.fstat(self.descriptor)
        except OSError as error:
            raise uf.FlowError('Python import cache binding was closed') from error
        expected = self.identity.st_dev, self.identity.st_ino
        if (current.st_dev, current.st_ino) != expected or not stat.S_ISDIR(current.st_mode):
            raise uf.FlowError('Python import cache binding changed')
        if named:
            try:
                lookup = self.directory.lstat()
            except OSError as error:
                raise uf.FlowError('Python import cache binding changed') from error
            if (lookup.st_dev, lookup.st_ino) != expected or not stat.S_ISDIR(lookup.st_mode):
                raise uf.FlowError('Python import cache binding changed')

    def activate(self):
        os.environ['PYTHONPYCACHEPREFIX'] = sys.pycache_prefix = self.prefix
        os.environ['PYTHONDONTWRITEBYTECODE'] = '1'
        sys.dont_write_bytecode = True

    def contents(self):
        rows = []
        def incomplete(error):
            raise uf.FlowError('Python import cache lost coverage') from error
        for directory, children, names in os.walk(self.directory, followlinks=False, onerror=incomplete):
            parent = Path(directory)
            for path in [parent, *(parent / name for name in children + names)]:
                expected = path.lstat()
                mode = expected.st_mode
                if stat.S_ISLNK(mode) or not (stat.S_ISDIR(mode) or stat.S_ISREG(mode)):
                    raise uf.FlowError('Python import cache contains an unsupported entry')
                rows.append((str(path.relative_to(self.directory)), mode,
                             artifact_digest(path, expected) if stat.S_ISREG(mode) else b''))
        return digest_rows(sorted(set(rows)))

    def __call__(self):
        self.bound()
        if (os.get_inheritable(self.descriptor) or sys.pycache_prefix != self.prefix or
                os.environ.get('PYTHONPYCACHEPREFIX') != self.prefix or
                not sys.dont_write_bytecode or os.environ.get('PYTHONDONTWRITEBYTECODE') != '1'):
            raise uf.FlowError('Python import cache policy changed')
        if self.watch is None and any(self.directory.iterdir()):
            raise uf.FlowError('Python import cache changed before preparation')
        if self.watch is not None:
            self.watch.check()
            if self.contents() != self.digest:
                raise uf.FlowError('Prepared Python import cache changed')
            self.watch.check()

    def prepare(self, root, directories, files):
        self()
        if self.watch is not None:
            raise uf.FlowError('Python import cache was already prepared')
        started = time.monotonic()
        sources = set()
        def incomplete(error):
            raise uf.FlowError('Python cache preparation lost source coverage') from error
        for directory in {root, *directories}:
            for parent, children, names in os.walk(directory, followlinks=False, onerror=incomplete):
                parent = Path(parent)
                children[:] = [name for name in children if not helper_output(name) and
                    not (directory == root and source_output((parent / name).relative_to(root).as_posix(), owned_runtime(root)))]
                sources.update(str(parent / name) for name in names if name.endswith('.py') and (parent / name).is_file())
        sources.update(str(path) for path in files if path.suffix == '.py' and path.is_file())
        # Compile supplied sources with safe paths and no site/package startup.
        # Retain selected PYTHONHOME; suppress only arbitrary PYTHONPATH imports.
        # Checked hashes bind each generated entry to its source, including aliases.
        program = ('import json,sys,py_compile; '
                   'paths=json.load(sys.stdin); '
                   '\nfor path in paths:\n'
                   ' try: py_compile.compile(path,doraise=True,invalidation_mode=py_compile.PycInvalidationMode.CHECKED_HASH)\n'
                   ' except py_compile.PyCompileError: pass\n')
        subprocess.run(['python3', '-P', '-S', '-B', '-X', 'pycache_prefix=' + self.prefix, '-c', program],
                       input=json.dumps(sorted(sources)), env={**os.environ, 'PYTHONPATH': ''},
                       text=True, check=True, timeout=120)
        self.watch = InputWatch(self.directory, lambda name: False)
        self.watch.external(self.directory)
        self.digest = self.contents()
        self()
        entries = [path for path in self.directory.rglob('*') if path.is_file()]
        return {'phase': 'python-cache-prepare', 'seconds': round(time.monotonic() - started, 3),
                'exit_code': 0, 'entries': len(entries), 'bytes': sum(path.stat().st_size for path in entries)}

    def close(self):
        try:
            if self.watch is not None:
                try:
                    self.watch.check()
                finally:
                    self.watch.close()
        finally:
            self.bound(named=False)
            os.close(self.descriptor)


@contextmanager
def python_source_imports(parent=None):
    """Prepare once, then read watched bytecode without automatic writes."""
    temporary = tempfile.TemporaryDirectory(prefix='python-import-cache-', dir=parent)
    try:
        cache = PythonCache(temporary.name)
        previous = (os.environ.get('PYTHONPYCACHEPREFIX'), sys.pycache_prefix,
                    os.environ.get('PYTHONDONTWRITEBYTECODE'), sys.dont_write_bytecode)
        try:
            cache.activate()
            cache()
            yield cache
        finally:
            original_error = sys.exception()
            old_env, sys.pycache_prefix, old_write, sys.dont_write_bytecode = previous
            for key, value in [('PYTHONPYCACHEPREFIX', old_env), ('PYTHONDONTWRITEBYTECODE', old_write)]:
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value
            try:
                cache.close()
            except BaseException:
                if original_error is None:
                    raise
    finally:
        original_error = sys.exception()
        try:
            temporary.cleanup()
        except BaseException:
            if original_error is None:
                raise


def verify(root, base, review_path, *, merge_base=False, summary=None, runner=None):
    from verification_timing import observe
    validate_loader_environment()
    validate_python_environment()
    if os.environ.get('ESBUILD_BINARY_PATH'):
        raise uf.FlowError('Custom ESBUILD_BINARY_PATH helpers are unsupported by local verification')
    executable_search_inputs()
    cache_parent = Path(os.environ.get('TMPDIR') or tempfile.gettempdir()).resolve()
    if cache_parent.is_relative_to(root):
        raise uf.FlowError('Owned local verification requires a temporary parent outside the checkout')
    publications = Publications()
    cancellation = InvocationCancellation()
    try:
        # This outer scope protects checked-scope restoration and rollback. Its
        # exit cannot introduce a new failure after the outcome has committed.
        with invocation_signals(cancellation, check_pending=False):
            try:
                with invocation_signals(cancellation), git_readonly():
                    with ExitStack() as imports:
                        with managed(root), go_build_cache() as go_cache:
                            with invocation_signals(cancellation):
                                imports_guard = imports.enter_context(python_source_imports(cache_parent))
                                with planning_source(root, review_path) as planning_guard:
                                    result = verify_owned(root, base, review_path, merge_base=merge_base, summary=summary,
                                                          runner=runner, cancellation=cancellation, publications=publications,
                                                          imports_guard=imports_guard, planning_guard=planning_guard,
                                                          go_cache=go_cache)
                        cancellation.check()
                        # managed() restores its entry environment after cleanup.
                        imports_guard.activate()
                        imports_guard()
                        cancellation.check()
                    cancellation.check()
                return result
            except BaseException as original_error:
                # Output descriptors remain live through cache disposal and the
                # checked scope's exit, including its pending-signal check.
                try:
                    publications.invalidate()
                except BaseException:
                    if isinstance(original_error, (KeyboardInterrupt, SystemExit)):
                        raise original_error
                    raise
                if not isinstance(original_error, (KeyboardInterrupt, SystemExit)):
                    cancellation.check()
                raise
    finally:
        # The outcome is committed; caller handlers are already restored.
        observe(publications.close)


def verify_owned(root, base, review_path, *, merge_base=False, summary=None, runner=None, cancellation=None,
                 publications=None, imports_guard=None, planning_guard=None, go_cache=None):
    """Execute the full native plan and all current required suites in one scope."""
    started = time.monotonic()
    cancellation = cancellation or InvocationCancellation()
    cancellation.check()
    if imports_guard is not None:
        imports_guard()
    validate_summary(root, review_path, summary)
    if os.environ.get('GITHUB_ACTIONS'):
        raise uf.FlowError('Owned local verification is not a hosted CI entrypoint')
    if os.environ.get('CODE_FACTORY_REAL_CODEX', '0') != '0':
        raise uf.FlowError('Owned local verification requires fixture mode')
    files = source_snapshot(root)
    original_ref = uf.revision(root, base)
    effective = uf.git(root, 'merge-base', original_ref, uf.revision(root, 'HEAD')).decode().strip() if merge_base else original_ref
    _, affected = specs.analyze(source_snapshot(root, effective), files, effective)
    review_bytes = review_content(review_path)
    reviews = uf.decode_json(review_bytes.decode())
    validate_review(affected, reviews)
    preparation = native_plan(root, 'local-verification-prepare', effective)
    if any(step.phase not in PREPARATION for step in preparation):
        raise uf.FlowError('Combined preparation contains an unsupported verification phase')
    plan = native_plan(root, 'verify-steps', effective)
    required = {tuple(command): expand(root, command, effective) for item in affected.values()
                for suite in item['suites'].values() for command in suite['commands']}
    all_steps = preparation + plan + [step for expansion in required.values() for step in expansion]
    cancellation.check()
    if planning_guard is not None:
        planning_guard()
    session = Session(root, effective, all_steps, runner=runner, go_cache=go_cache)
    def guard():
        cancellation.check()
        if planning_guard is not None:
            planning_guard()
        if imports_guard is not None:
            imports_guard()
        if review_content(review_path) != review_bytes or uf.revision(root, base) != original_ref:
            raise uf.FlowError('Review decisions or comparison changed during verification')
    session.guard = guard
    paths = [root / specs.RECORDS / (subject + '.json') for subject in reviews]
    success = False
    try:
        if imports_guard is not None:
            session.rows.append(imports_guard.prepare(root, session.helper_directories, session.helper_files))
        session.assert_current()
        session.watch.external(review_path)
        for path in paths:
            path.unlink(missing_ok=True)
        prepare_once(session, all_steps)
        session.freeze()
        for step in plan:
            if step.phase not in PREPARATION and step.phase != 'specifications':
                session.execute(step)
        def execute_required(command, **options):
            if options['cwd'] != root:
                raise uf.FlowError('Unexpected reconciliation working directory')
            seconds = sum(session.execute(step)['seconds'] for step in required[tuple(command)])
            result = subprocess.CompletedProcess(command, 0)
            result.verification_seconds = round(seconds, 3)
            return result
        if reviews:
            reconcile(root, files, affected, effective, review_path, records=specs.RECORDS,
                         analyzer=specs.analyze, constraints=specs.constraints, executor=execute_required,
                         review_bytes=review_bytes, source_snapshot=source_snapshot)
        session.assert_current()
        if review_content(review_path) != review_bytes or uf.revision(root, base) != original_ref:
            raise uf.FlowError('Review decisions or comparison changed during verification')
        checks = [step for step in plan if step.phase == 'specifications']
        if len(checks) != 1 or checks[0].command != ('python3', 'scripts/specs.py', 'check', '--base', effective):
            raise uf.FlowError('Native verification must include the exact final specification check')
        session.execute(checks[0])
        session.assert_current()
        if session.versions != session.tool_versions():
            raise uf.FlowError('Effective toolchain versions changed during verification')
        session.assert_current()
        if publications is not None:
            for path in paths:
                publications.capture(path)
        session.publications = publications
        success = True
        return {'verified': True, 'reconciled': sorted(reviews), 'commands': len(session.rows),
                'wall_seconds': round(time.monotonic() - started, 3)}
    finally:
        # The outer handlers already record signals: entering cleanup cannot
        # throw before watches and receipt invalidation have been attempted.
        original_error = sys.exception()
        cleanup_error = None
        try:
            session.close()
        except BaseException as error:
            cleanup_error = error
        if not success or cleanup_error is not None or cancellation.number is not None:
            success = False
            for path in paths:
                try:
                    path.unlink(missing_ok=True)
                except BaseException as error:
                    cleanup_error = cleanup_error or error
        elapsed = round(time.monotonic() - started, 3)
        if summary:
            from verification_timing import observe
            observe(write_summary, summary, session, success, effective, original_ref, merge_base, elapsed)
        if imports_guard is not None:
            try:
                imports_guard()
            except BaseException as error:
                cleanup_error = cleanup_error or error
        if cleanup_error is not None and original_error is None and cancellation.number is None:
            raise cleanup_error


def write_summary(summary, session, success, effective, original_ref, merge_base, elapsed):
    summary.parent.mkdir(parents=True, exist_ok=True)
    content = json.dumps({'version': 1, 'success': success, 'base': effective,
        'head': session.head,
        'source': {'head': session.head, 'inputs_sha256': session.source},
        'prepared_inputs_sha256': session.prepared,
        'comparison': {'base': effective, 'requested_base': original_ref, 'merge_base': merge_base},
        'toolchains': {'executables_sha256': session.tools, 'versions_sha256': session.versions},
        'environment_sha256': digest_rows((os.fsencode(k), os.fsencode(v))
            for k, v in sorted({**session.environment, **uf.FIXTURE_ENV}.items())),
        'commands': len(session.rows), 'phases': session.rows,
        'wall_seconds': elapsed}, indent=2) + '\n'
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', dir=summary.parent, prefix='.' + summary.name + '-',
                                         delete=False) as output:
            temporary = Path(output.name)
            output.write(content)
        publications = getattr(session, 'publications', None)
        if publications is not None and success:
            publications.capture(temporary)
        temporary.replace(summary)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument('--base', required=True)
    parser.add_argument('--merge-base', action='store_true')
    parser.add_argument('--review', type=Path, required=True)
    parser.add_argument('--summary', type=Path)
    args = parser.parse_args(argv)
    root = args.root.resolve()
    summary = args.summary.resolve() if args.summary else root / 'verification-results' / ('local-' + str(os.getpid()) + '.json')
    try:
        print(json.dumps(verify(root, args.base, args.review, merge_base=args.merge_base, summary=summary)))
        return 0
    except (uf.FlowError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(str(error), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
