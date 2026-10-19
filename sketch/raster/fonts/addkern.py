# Copies the pair kerning of a font's GPOS 'kern' feature into a legacy 'kern'
# table on its subset, for the pairs of characters the subset keeps.
# Usage: python3 -I addkern.py FULL.ttf SUBSET.ttf OUT.ttf
import sys
from fontTools.ttLib import TTFont, newTable
from fontTools.ttLib.tables._k_e_r_n import KernTable_format_0

full = TTFont(sys.argv[1])
sub = TTFont(sys.argv[2])
cmap_full = full.getBestCmap()
cmap_sub = sub.getBestCmap()
# Pairs among printable ASCII and Latin-1 letters.
chars = [c for c in list(range(0x20, 0x7f)) + list(range(0xc0, 0x100)) if c in cmap_full and c in cmap_sub]
glyph_of = {c: cmap_full[c] for c in chars}
wanted = set(glyph_of.values())

gpos = full['GPOS'].table
lookups = set()
for fr in gpos.FeatureList.FeatureRecord:
    if fr.FeatureTag == 'kern':
        lookups.update(fr.Feature.LookupListIndex)
pairs = {}
def add(a, b, v):
    if v and (a, b) not in pairs and a in wanted and b in wanted:
        pairs[(a, b)] = v
for li in sorted(lookups):
    lk = gpos.LookupList.Lookup[li]
    subs = lk.SubTable
    if lk.LookupType == 9:
        subs = [s.ExtSubTable for s in subs]
    for st in subs:
        if getattr(st, 'LookupType', 2) != 2 and lk.LookupType not in (2, 9):
            continue
        cov = st.Coverage.glyphs
        if st.Format == 1:
            for i, first in enumerate(cov):
                for pvr in st.PairSet[i].PairValueRecord:
                    v = pvr.Value1.XAdvance if pvr.Value1 and hasattr(pvr.Value1, 'XAdvance') else 0
                    add(first, pvr.SecondGlyph, v)
        elif st.Format == 2:
            c1 = st.ClassDef1.classDefs
            c2 = st.ClassDef2.classDefs
            for first in cov:
                k1 = c1.get(first, 0)
                for second in wanted:
                    k2 = c2.get(second, 0)
                    rec = st.Class1Record[k1].Class2Record[k2]
                    v = rec.Value1.XAdvance if rec.Value1 and hasattr(rec.Value1, 'XAdvance') else 0
                    add(first, second, v)
# The subset keeps glyph names of the full font for these glyphs.
names = set(sub.getGlyphOrder())
kt = KernTable_format_0()
kt.version = 0
kt.coverage = 1
kt.kernTable = {p: v for p, v in pairs.items() if p[0] in names and p[1] in names}
kern = newTable('kern')
kern.version = 0
kern.kernTables = [kt]
sub['kern'] = kern
sub.save(sys.argv[3])
print(len(kt.kernTable), 'pairs')
