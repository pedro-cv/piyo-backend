"""Independent cross-check of the app's RoutePlanner on a published dataset package.

Re-implements the planner rules (docs/03 §4, app/domain/routing/RoutePlanner.kt) in Python
without sharing code: snap origin/destination to the nearest polyline vertex within 400 m,
direct itineraries on one variant, one-transfer itineraries through the precomputed links,
costs walking 4.5 km/h, micro 18 km/h, +6 min per transfer, fare = tariff x lines.
Prints, for each trip, the candidate variants and the ranked itineraries, so the numbers the
app shows can be checked by hand against the data.

Usage: python check_trips.py <dataset-vN.json(.gz)> [trip ...]
A trip is  name|lat,lng|lat,lng ; default trips are defined below.
"""
import gzip, json, math, sys
from collections import defaultdict

R = 6371000.0

def dist(a, b):
    la1, lo1, la2, lo2 = map(math.radians, (a[0], a[1], b[0], b[1]))
    h = math.sin((la2-la1)/2)**2 + math.cos(la1)*math.cos(la2)*math.sin((lo2-lo1)/2)**2
    return 2*R*math.atan2(math.sqrt(h), math.sqrt(1-h))

def decode(s):
    out, i, lat, lng = [], 0, 0, 0
    while i < len(s):
        for which in (0, 1):
            result = shift = 0
            while True:
                b = ord(s[i]) - 63; i += 1
                result |= (b & 0x1f) << shift; shift += 5
                if b < 0x20 or i >= len(s): break
            d = ~(result >> 1) if result & 1 else (result >> 1)
            if which == 0: lat += d
            else: lng += d
            if i >= len(s) and which == 0: break
        out.append((lat/1e5, lng/1e5))
    return out

def load(path):
    raw = gzip.open(path).read() if path.endswith(".gz") else open(path, "rb").read()
    d = json.loads(raw)
    lines = {l["line_id"]: l for l in d["lines"]}
    variants = {}
    for v in d["variants"]:
        v = dict(v); v["pts"] = decode(v["polyline"]); variants[v["variant_id"]] = v
    links = defaultdict(list)
    order = [v["variant_id"] for v in d["variants"]]
    for t in d["transfers"]:
        if isinstance(t, list):  # package format 2: [from_var, from_idx, to_var, to_idx, walk_m]
            t = {"from": order[t[0]], "from_idx": t[1], "to": order[t[2]], "to_idx": t[3], "walk_m": t[4]}
        links[(t["from"], t["to"])].append(t)
    return d, lines, variants, links

def snap(p, pts):
    best = min(range(len(pts)), key=lambda i: dist(p, pts[i]))
    return best, dist(p, pts[best])

def walk_min(m): return max(1, round(m/75.0))
def ride_min(m): return max(1, round(m/300.0))
def path_len(pts): return sum(dist(pts[i-1], pts[i]) for i in range(1, len(pts)))

def plan(d, lines, variants, links, o, dest, max_walk=400, penalty=6):
    fare = float(d["fare_bs"])
    fromc, toc = [], []
    for v in variants.values():
        i, m = snap(o, v["pts"])
        if m <= max_walk: fromc.append((v, i, m))
        j, m2 = snap(dest, v["pts"])
        if m2 <= max_walk: toc.append((v, j, m2))
    its = []
    def mk(legs_rides):
        cursor, walk, minutes, segs = o, 0, 0, []
        for k, (v, i, j, link_walk) in enumerate(legs_rides):
            w = round(dist(cursor, v["pts"][i])); wm = walk_min(w); walk += w; minutes += wm
            seg = v["pts"][i:j+1]; rm = path_len(seg); rmin = ride_min(rm); minutes += rmin
            segs.append(f"walk {w} m ({wm} min) -> {lines[v['line_id']]['display']} [{v['variant_id']} idx {i}->{j}] {rm/1000:.1f} km ({rmin} min)")
            cursor = v["pts"][j]
            if k < len(legs_rides)-1: minutes += penalty
        w = round(dist(cursor, dest)); wm = walk_min(w); walk += w; minutes += wm
        segs.append(f"walk {w} m ({wm} min)")
        return dict(minutes=minutes, walk=walk, transfers=len(legs_rides)-1,
                    fare=fare*len(legs_rides), lines="+".join(lines[v['line_id']]['display'] for v,_,_,_ in legs_rides), segs=segs)
    for (va, ia, _) in fromc:
        for (vb, jb, _) in toc:
            if va["variant_id"] == vb["variant_id"] and jb > ia:
                its.append(mk([(va, ia, jb, 0)]))
    for (va, ia, _) in fromc:
        for (vb, jb, _) in toc:
            if va["line_id"] == vb["line_id"]: continue
            cands = [t for t in links.get((va["variant_id"], vb["variant_id"]), []) if t["from_idx"] > ia and t["to_idx"] < jb]
            if not cands: continue
            t = min(cands, key=lambda t: t["walk_m"])
            its.append(mk([(va, ia, t["from_idx"], 0), (vb, t["to_idx"], jb, t["walk_m"])]))
    its.sort(key=lambda it: (it["transfers"], it["minutes"]))  # app ranks fewer rides first (2026-10-31)
    seen, out = set(), []
    for it in its:
        if it["lines"] in seen: continue
        seen.add(it["lines"]); out.append(it)
        if len(out) == 3: break
    return fromc, toc, out

DEFAULT_TRIPS = [
    # name | origin lat,lng | destination lat,lng   (coordinates taken from the dataset's own stops/polylines, see EVIDENCE.md)
    "Plaza 24 de Septiembre -> Mall Ventura|-17.7834,-63.1821|-17.7572,-63.1963",
    "Transportadora 26 de Enero (Vallegrande) -> Parque Urbano|-17.805495,-63.198729|-17.7765,-63.1826",
    "Mercado Abasto -> Aeropuerto Viru Viru|-17.8078,-63.1575|-17.6448,-63.1354",
    "UAGRM campus -> Mercado Mutualista|-17.7757,-63.1933|-17.7660,-63.1540",
]

def main():
    if len(sys.argv) < 2:
        print(__doc__); sys.exit(1)
    d, lines, variants, links = load(sys.argv[1])
    print(f"dataset v{d['dataset_version']}: {len(lines)} lines, {len(variants)} variants, {sum(len(v) for v in links.values())} transfer links")
    trips = sys.argv[2:] or DEFAULT_TRIPS
    for t in trips:
        name, o, de = t.split("|")
        o = tuple(map(float, o.split(","))); de = tuple(map(float, de.split(",")))
        fromc, toc, its = plan(d, lines, variants, links, o, de)
        print(f"\n== {name}\n   origin {o} -> destination {de}, straight line {dist(o, de)/1000:.1f} km")
        print("   variants within 400 m of origin:      " + ", ".join(f"{lines[v['line_id']]['display']}({v['variant_id']},{round(m)} m)" for v,_,m in sorted(fromc, key=lambda x: x[2])))
        print("   variants within 400 m of destination: " + ", ".join(f"{lines[v['line_id']]['display']}({v['variant_id']},{round(m)} m)" for v,_,m in sorted(toc, key=lambda x: x[2])))
        if not its: print("   -> no itinerary (no shared variant and no transfer link)")
        for k, it in enumerate(its, 1):
            print(f"   #{k} {it['lines']}: {it['minutes']} min, walk {it['walk']} m, {it['transfers']} transfer(s), Bs {it['fare']:g}")
            for s in it["segs"]: print("       " + s)

if __name__ == "__main__":
    main()
