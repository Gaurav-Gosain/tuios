#!/usr/bin/env python3
# security-templates.py PELE_DIR > security_templates_test.go: print Go template lists translated from pele's
# tests/render/{type}-security.test.ts TEMPLATES arrays.
import re, sys
pele = sys.argv[1]
names = {'sequence': 'sequenceTemplates', 'state': 'stateTemplates', 'class': 'classTemplates',
         'er': 'erTemplates', 'gantt': 'ganttTemplates', 'mindmap': 'mindmapTemplates'}
exprs = {'p': 'p', 'json(p)': 'jsonText(p)', 'HEAD': 'ganttHead', 'TASKS': 'ganttTasks'}

def lit(s):
    out, i = [], 0
    while i < len(s):
        c = s[i]
        if c == '\\':
            n = s[i+1]
            out.append({'n': '\\n', 't': '\\t', '`': '`', '\\': '\\\\', '$': '$', '"': '\\"'}[n]); i += 2; continue
        out.append('\\"' if c == '"' else c); i += 1
    return ''.join(out)

def go(body):
    parts = re.split(r'\$\{([^}]*)\}', body)
    pieces = []
    for k, part in enumerate(parts):
        if k % 2:
            pieces.append(exprs[part])
        elif part:
            pieces.append('"' + lit(part) + '"')
    return ' + '.join(pieces) if pieces else '""'

print('// Code generated from pele tests/render/*-security.test.ts at 458322a5. DO NOT EDIT.\n\npackage sketch\n')
for typ, var in names.items():
    src = open(f'{pele}/tests/render/{typ}-security.test.ts').read()
    block = src[src.index('const TEMPLATES'):]
    block = block[:block.index('\n];')]
    bodies = re.findall(r'\(p\) =>\s*`((?:[^`\\]|\\.)*)`', block, re.S)
    print(f'var {var} = []func(p string) string{{')
    for b in bodies:
        print(f'\tfunc(p string) string {{ return {go(b)} }},')
    print('}\n')
    print(f'// {typ}: {len(bodies)} templates', file=sys.stderr)
