#!/usr/bin/env python3
# security-templates.py PELE_DIR > security_templates_test.go: print Go template lists translated from pele's
# tests/render/{type}-security.test.ts TEMPLATES arrays (swimlane's are in swimlane.test.ts).
import re, sys
pele = sys.argv[1]
names = {'sequence': 'sequenceTemplates', 'state': 'stateTemplates', 'class': 'classTemplates',
         'er': 'erTemplates', 'gantt': 'ganttTemplates', 'mindmap': 'mindmapTemplates',
         'xychart': 'xychartTemplates', 'timeline': 'timelineTemplates', 'quadrant': 'quadrantTemplates',
         'journey': 'journeyTemplates', 'kanban': 'kanbanTemplates', 'git': 'gitTemplates',
         'sankey': 'sankeyTemplates', 'block': 'blockTemplates', 'architecture': 'architectureTemplates',
         'requirement': 'requirementTemplates', 'c4': 'c4Templates', 'packet': 'packetTemplates',
         'radar': 'radarTemplates', 'treemap': 'treemapTemplates', 'info': 'infoTemplates',
         'treeview': 'treeviewTemplates', 'ishikawa': 'ishikawaTemplates',
         'cynefin': 'cynefinTemplates', 'eventmodeling': 'eventmodelingTemplates',
         'venn': 'vennTemplates', 'wardley': 'wardleyTemplates',
         'railroad': 'railroadTemplates', 'swimlane': 'swimlaneTemplates',
         'usecase': 'usecaseTemplates', 'agentflow': 'agentflowTemplates'}
exprs = {'p': 'p', 'json(p)': 'jsonText(p)', 'HEAD': 'ganttHead', 'TASKS': 'ganttTasks', 'BASE': 'kanbanBase', 'q': 'q', 'BODY': 'sankeyBody', 'quoted(p)': 'sankeyQuoted(p)', 'head': 'archHead', 'TWO': 'c4Two', 'I': 'ishikawaHead', 'V': 'vennHead',
         "p.replace(/[\"\\\\\\n]/g, '')": 'gitStrip(p)',
         "p.replace(/'/g, '')": 'stripApostrophes(p)', "p.replace(/[\"\\\\]/g, '')": 'stripQuotes(p)',
         'JSON.stringify(p)': 'jsonText(p)'}

# Types whose templates are in their render test, which has no security file.
files = {'swimlane': 'swimlane.test.ts'}

# Names that mean something else in one type's test.
type_exprs = {'requirement': {'BODY': 'requirementBody'},
              'cynefin': {'quoted(p)': 'cynefinQuoted(p)', 'single(p)': 'cynefinSingle(p)'},
              'eventmodeling': {'line(p)': 'emLine(p)'},
              'wardley': {'quoted(p)': 'cynefinQuoted(p)', 'single(p)': 'cynefinSingle(p)'},
              'railroad': {'quoted(p)': 'railroadQuoted(p)', 'bare(p)': 'railroadBare(p)', "p.replace(/[?;]/g, '')": 'railroadNoSpecial(p)',
                           "p.replace(/\\\\/g, '\\\\\\\\').replace(/'/g, \"\\\\'\")": 'railroadSingle(p)'},
              'usecase': {'U': 'usecaseHead'},
              'agentflow': {'H': 'agentflowHead'}}

def lit(s):
    out, i = [], 0
    while i < len(s):
        c = s[i]
        if c == '\\':
            n = s[i+1]
            out.append({'n': '\\n', 't': '\\t', '`': '`', '\\': '\\\\', '$': '$', '"': '\\"'}[n]); i += 2; continue
        out.append('\\"' if c == '"' else c); i += 1
    return ''.join(out)

def go(body, typ):
    parts = re.split(r'\$\{([^}]*)\}', body)
    pieces = []
    for k, part in enumerate(parts):
        if k % 2:
            own = type_exprs.get(typ, {})
            pieces.append(own[part] if part in own else exprs[part])
        elif part:
            pieces.append('"' + lit(part) + '"')
    return ' + '.join(pieces) if pieces else '""'

print('// Code generated from pele tests/render/*-security.test.ts at 8fad23bd. DO NOT EDIT.\n\npackage sketch\n')
for typ, var in names.items():
    src = open(f'{pele}/tests/render/' + files.get(typ, f'{typ}-security.test.ts')).read()
    block = src[src.index('const TEMPLATES'):]
    block = block[:block.index('\n];')]
    # git's templates take the payload quoted (q) and as written (p).
    two = typ == 'git'
    bodies = re.findall(r'\((?:_?q, p|_?q|p)\) =>\s*`((?:[^`\\]|\\.)*)`', block, re.S)
    sig = 'q, p string' if two else 'p string'
    print(f'var {var} = []func({sig}) string{{')
    for b in bodies:
        print(f'\tfunc({sig}) string {{ return {go(b, typ)} }},')
    print('}\n')
    print(f'// {typ}: {len(bodies)} templates', file=sys.stderr)
