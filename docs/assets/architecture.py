#!/usr/bin/env python3
"""Renders docs/assets/architecture.gif: how one command travels through kbridge.

    python3 docs/assets/architecture.py            # needs Pillow and DejaVu fonts

Two commands are shown as they behave in v0.3.0-alpha.1: one allowed and run as
the user through impersonation, one refused at the control plane and recorded.
Edit the timeline in scene() when the flow changes, then re-run.
"""
import os
import textwrap

from PIL import Image, ImageDraw, ImageFont

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "architecture.gif")
W, H, FPS, LENGTH = 1280, 640, 10, 21

BG, PANEL, LINE, TEXT, DIM = "#1e1e2e", "#313244", "#45475a", "#cdd6f4", "#7f849c"
BLUE, MAUVE, GREEN, RED, YELLOW = "#89b4fa", "#cba6f7", "#a6e3a1", "#f38ba8", "#f9e2af"
FONTS = "/usr/share/fonts/truetype/dejavu/"
SANS = lambda s: ImageFont.truetype(FONTS + "DejaVuSans.ttf", s)
BOLD = lambda s: ImageFont.truetype(FONTS + "DejaVuSans-Bold.ttf", s)
MONO = lambda s: ImageFont.truetype(FONTS + "DejaVuSansMono.ttf", s)

LAP, CP, DB = (40, 110, 340, 330), (460, 110, 340, 330), (460, 470, 340, 130)
ST, PR, KA = (880, 110, 360, 160), (880, 300, 360, 160), (880, 490, 360, 110)
USER, CLUSTER = "dev@example.com", "staging"


def between(t, a, b):
    """Progress of t through [a, b], clamped to 0..1."""
    return min(1.0, max(0.0, (t - a) / (b - a)))


def box(d, r, border=LINE, fill=PANEL):
    x, y, w, h = r
    d.rounded_rectangle((x, y, x + w, y + h), 10, fill=fill, outline=border, width=2)


def label(d, xy, s, font, fill=TEXT, anchor="la"):
    d.text(xy, s, font=font, fill=fill, anchor=anchor)


def dot(d, path, p, color, r=6):
    """A dot at fraction p along a polyline path."""
    segs = list(zip(path, path[1:]))
    total = sum(abs(b[0] - a[0]) + abs(b[1] - a[1]) for a, b in segs) or 1
    want = p * total
    for a, b in segs:
        n = abs(b[0] - a[0]) + abs(b[1] - a[1])
        if want <= n:
            f = want / n if n else 0
            x, y = a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f
            d.ellipse((x - r, y - r, x + r, y + r), fill=color)
            return
        want -= n


def check(d, xy, state):
    """A status mark: None pending, True ok, False refused."""
    x, y = xy
    if state is None:
        d.ellipse((x - 5, y - 5, x + 5, y + 5), outline=DIM, width=2)
    elif state:
        d.line([(x - 6, y), (x - 2, y + 5), (x + 7, y - 6)], fill=GREEN, width=3)
    else:
        d.line([(x - 6, y - 6), (x + 6, y + 6)], fill=RED, width=3)
        d.line([(x - 6, y + 6), (x + 6, y - 6)], fill=RED, width=3)


def laptop(d, lines):
    x, y, w, h = LAP
    box(d, LAP)
    label(d, (x + 20, y + 18), "Developer laptop", BOLD(17))
    label(d, (x + w - 20, y + 20), "kb CLI", MONO(13), BLUE, "ra")
    d.rounded_rectangle((x + 20, y + 56, x + w - 20, y + h - 60), 8, fill=BG)
    for i, (s, c) in enumerate(lines[-11:]):
        label(d, (x + 32, y + 68 + i * 18), s, MONO(13), c)
    label(d, (x + 20, y + h - 36), "no kubeconfig, no cluster credential", SANS(13), DIM)


def control_plane(d, checks, border=LINE):
    x, y, w, h = CP
    box(d, CP, border)
    label(d, (x + 20, y + 18), "Control plane", BOLD(17))
    label(d, (x + 20, y + 44), "authenticate · authorize · route", SANS(13), DIM)
    for i, (name, state) in enumerate(checks):
        ry = y + 80 + i * 52
        d.rounded_rectangle((x + 20, ry, x + w - 20, ry + 40), 8, fill=BG)
        label(d, (x + 36, ry + 12), name, SANS(14))
        check(d, (x + w - 44, ry + 20), state)
    label(d, (x + 20, y + h - 36), "one-shot queue · live streams", SANS(13), DIM)


def database(d, rows):
    x, y, w, h = DB
    box(d, DB)
    label(d, (x + 20, y + 14), "SQLite", BOLD(15))
    label(d, (x + w - 20, y + 16), "audit of " + USER, SANS(12), DIM, "ra")
    for i, (s, c) in enumerate(rows[-3:]):
        label(d, (x + 20, y + 44 + i * 20), s, MONO(11), c)


def cluster(d, r, name, connected, active, who=None):
    x, y, w, h = r
    box(d, r, GREEN if active else LINE)
    label(d, (x + 20, y + 16), name, BOLD(16))
    label(d, (x + w - 20, y + 18), "private network", SANS(12), DIM, "ra")
    a, k = (x + 20, y + 50, 130, 56), (x + 210, y + 50, 130, 56)
    box(d, a, GREEN if active else LINE, BG)
    label(d, (a[0] + 65, a[1] + 20), "kbridge agent", SANS(13), anchor="mm")
    label(d, (a[0] + 65, a[1] + 40), "1 pod", SANS(11), DIM, "mm")
    box(d, k, LINE, BG)
    label(d, (k[0] + 65, k[1] + 20), "kube-apiserver", SANS(13), anchor="mm")
    label(d, (k[0] + 65, k[1] + 40), "cluster RBAC", SANS(11), DIM, "mm")
    d.line([(x + 150, y + 78), (x + 206, y + 78)], fill=GREEN if active else LINE, width=2)
    d.polygon([(x + 210, y + 78), (x + 200, y + 72), (x + 200, y + 84)], fill=GREEN if active else LINE)
    label(d, (x + 180, y + 64), "kubectl", MONO(11), DIM, "mm")
    c = GREEN if connected else DIM
    d.ellipse((x + 22, y + 124, x + 30, y + 132), fill=c)
    label(d, (x + 38, y + 121), "connected" if connected else "connecting", SANS(12), c)
    if who:
        label(d, (x + w - 20, y + 121), "as " + who, MONO(12), YELLOW, "ra")
    else:
        label(d, (x + w - 20, y + 121), "RoleBindings in sync", SANS(12), DIM, "ra")


def links(d, connected, hot=None):
    """Laptop→control plane, and control plane→each agent. hot colours one link."""
    for key, path, name, sub in (
        ("lap", [(380, 275), (460, 275)], "HTTPS", ""),
        ("st", [(800, 188), (880, 188)], "gRPC", "agent dials out"),
        ("pr", [(800, 378), (880, 378)], "gRPC", "agent dials out"),
    ):
        c = hot[1] if hot and hot[0] == key else (BLUE if connected or key == "lap" else DIM)
        d.line(path, fill=c, width=2)
        mx = (path[0][0] + path[1][0]) // 2
        label(d, (mx, path[0][1] - 10), name, SANS(11), c, "mm")
        if sub:
            label(d, (mx, path[0][1] + 12), sub, SANS(10), DIM, "mm")


def header(d, phase):
    label(d, (40, 36), "kbridge", BOLD(26))
    label(d, (170, 44), phase, SANS(15), DIM)
    for i, (c, s) in enumerate(((BLUE, "connection"), (MAUVE, "command"), (GREEN, "result"), (RED, "refused"))):
        x = 900 + i * 90
        d.ellipse((x, 42, x + 10, 52), fill=c)
        label(d, (x + 16, 40), s, SANS(12), DIM)
    d.line([(40, 80), (W - 40, 80)], fill=LINE, width=1)


def wrap(s, color, width=38):
    return [(line, color) for line in textwrap.wrap(s, width)]


def scene(t):
    im = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(im)
    connected = t >= 2.5
    checks = [("User identity", None), ("Role rules", None), ("Guardrails", None)]
    term, rows, hot, who, active, border, phase = [], [], None, None, False, LINE, "agents connect"

    # 0-2.5 s: agents dial out to the control plane.
    if t < 2.5:
        p = between(t, 0.3, 2.3)
        links(d, False)
        for path in ([(880, 188), (800, 188)], [(880, 378), (800, 378)]):
            dot(d, path, p, BLUE)
    else:
        links(d, True)

    # 3-12 s: an allowed command, run as the user.
    if t >= 3:
        phase = "kb get pods -n team-a"
        typed = "kb get pods -n team-a"[: int(between(t, 3, 4.5) * 21)]
        term.append(("$ " + typed, TEXT))
    if 4.5 <= t < 5.5:
        dot(d, [(380, 275), (460, 275)], between(t, 4.5, 5.5), MAUVE)
    if t >= 5.5:
        checks = [("User identity", True if t >= 5.8 else None),
                  ("Role rules", True if t >= 6.4 else None),
                  ("Guardrails", True if t >= 7.0 else None)]
    if 7.2 <= t < 8.2:
        hot = ("st", MAUVE)
        dot(d, [(800, 188), (880, 188)], between(t, 7.2, 8.2), MAUVE)
    if 8.2 <= t < 12:
        active, who = True, "kbridge:" + USER
    if 8.2 <= t < 9.0:
        dot(d, [(1030, 188), (1090, 188)], between(t, 8.2, 9.0), MAUVE)
    if 9.0 <= t < 10.5:
        hot = ("st", GREEN)
        dot(d, [(1090, 188), (880, 188), (800, 188)], between(t, 9.0, 9.8), GREEN)
        if t >= 9.8:
            dot(d, [(460, 275), (380, 275)], between(t, 9.8, 10.5), GREEN)
    if t >= 10.5:
        term += [("NAME        READY  STATUS", DIM), ("web-5f6d-x  1/1    Running", TEXT),
                 ("web-5f6d-y  1/1    Running", TEXT)]
        rows.append((f"{CLUSTER}  success  get pods -n team-a", GREEN))

    # 12-21 s: a refused command never reaches a cluster.
    if t >= 12:
        phase = "kb get secrets -n kube-system"
        typed = "kb get secrets -n kube-system"[: int(between(t, 12, 13.6) * 29)]
        term += [("", TEXT), ("$ " + typed, TEXT)]
        checks = [("User identity", None), ("Role rules", None), ("Guardrails", None)]
    if 13.6 <= t < 14.6:
        dot(d, [(380, 275), (460, 275)], between(t, 13.6, 14.6), MAUVE)
    if t >= 14.6:
        checks = [("User identity", True if t >= 14.9 else None),
                  ("Role rules", False if t >= 15.5 else None), ("Guardrails", None)]
    if t >= 15.5:
        border = RED
        label(d, (CP[0] + CP[2] // 2, CP[1] + CP[3] + 16), "never reaches a cluster", SANS(12), RED, "mm")
    if 15.8 <= t < 16.6:
        dot(d, [(460, 275), (380, 275)], between(t, 15.8, 16.6), RED)
    if t >= 16.6:
        term += wrap('Error: permission denied: get secrets in namespace "kube-system" on cluster '
                     f'"{CLUSTER}" is not allowed for {USER}; ask a kbridge admin to extend your role', RED, 36)
        rows.append((f"{CLUSTER}  denied   get secrets -n kube-system", RED))

    header(d, phase)
    laptop(d, term)
    control_plane(d, checks, border)
    database(d, rows)
    cluster(d, ST, CLUSTER, connected, active, who)
    cluster(d, PR, "production", connected, False)
    box(d, KA)
    label(d, (KA[0] + 20, KA[1] + 16), "Cluster audit log", BOLD(15))
    label(d, (KA[0] + 20, KA[1] + 44), "user: kbridge:" + USER if who or t >= 10.5 else "user: kbridge:<email>",
          MONO(12), YELLOW if who else DIM)
    label(d, (KA[0] + 20, KA[1] + 66), "the user, not a shared ServiceAccount", SANS(12), DIM)
    return im


def main():
    frames = [scene(i / FPS) for i in range(LENGTH * FPS)]
    frames[0].save(OUT, save_all=True, append_images=frames[1:], duration=1000 // FPS, loop=0, optimize=True)
    print(OUT, os.path.getsize(OUT), "bytes")


if __name__ == "__main__":
    main()
