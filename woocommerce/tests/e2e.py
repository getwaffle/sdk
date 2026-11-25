"""End to end: real WooCommerce (docker compose up) against a local Waffle stack (scripts/dev.sh start).
Usage: WAFFLE_KEY=wf_sandbox_... WAFFLE_EMAIL=... WAFFLE_PASSWORD=... python3 tests/e2e.py [waffle_repo]"""
import hashlib, hmac, http.cookiejar, json, os, subprocess, sys, time, urllib.error, urllib.request

API = "http://localhost:8080"
WP = "http://localhost:8899"
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WAFFLE = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "..", "waffle")
jar = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
fails = []


def call(method, url, body=None, headers=None, opener=None):
    data = body if isinstance(body, bytes) else None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data, {"Content-Type": "application/json", "Origin": "http://localhost:3001", **(headers or {})}, method=method)
    try:
        with (opener or urllib.request.build_opener()).open(req) as r:
            raw = r.read()
            return r.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:300]


def php(code):
    out = subprocess.run(["docker", "compose", "run", "--rm", "-T", "cli", "wp", "eval", code], cwd=ROOT, capture_output=True, text=True)
    if out.returncode:
        raise SystemExit(out.stderr + out.stdout)
    return out.stdout.strip()


def sql(q):
    subprocess.run(["docker", "compose", "-f", "docker-compose.dev.yml", "exec", "-T", "postgres", "psql", "-U", "postgres", "-d", "waffle", "-qc", q], cwd=WAFFLE, check=True, capture_output=True)


def check(name, ok, extra=""):
    print(("ok   " if ok else "FAIL ") + name, "" if ok else extra)
    if not ok:
        fails.append(name)


def order(total=50000):
    return int(php(f"$p=new WC_Product_Simple(); $p->set_name('x'); $p->set_regular_price('{total}'); $p->save(); $o=wc_create_order(); $o->add_product($p); $o->set_payment_method('waffle'); $o->calculate_totals(); $o->save(); echo $o->get_id();"))


def start(oid):
    return json.loads(php(f"wc_load_cart(); $g=new Waffle_Gateway(); echo wp_json_encode($g->process_payment({oid}));"))


def state(oid):
    return json.loads(php(f"$o=wc_get_order({oid}); echo wp_json_encode(['s'=>$o->get_status(),'t'=>$o->get_transaction_id(),'c'=>$o->get_meta('_waffle_charge_id'),'n'=>count(wc_get_order_notes(['order_id'=>{oid}]))]);"))


def wait(oid, status, secs=60):
    end = time.time() + secs
    while time.time() < end:
        s = state(oid)
        if s["s"] == status:
            return s
        time.sleep(2)
    return state(oid)


def pay(cid, **sel):
    a = call("POST", f"{API}/v1/checkout/{cid}/select-channel", sel)
    b = call("POST", "http://localhost:8095/mock/simulate", {"reference": cid, "status": "paid"})
    if a[0] != 200 or b[0] != 200:
        raise SystemExit(f"pay failed: {a} {b}")


def deliver(secret, body, t=None):
    t = str(int(time.time()) if t is None else t)
    raw = json.dumps(body).encode()
    sig = hmac.new(secret.encode(), t.encode() + b"." + raw, hashlib.sha256).hexdigest()
    return call("POST", WP + "/wp-json/waffle/v1/webhook", raw, {"X-Waffle-Signature-V2": f"t={t},v1={sig}"})[0]


s, r = call("POST", API + "/v1/merchants/login", {"email": os.environ["WAFFLE_EMAIL"], "password": os.environ["WAFFLE_PASSWORD"]}, opener=jar)
if s != 200:
    raise SystemExit(f"login: {s} {r}")
s, r = call("POST", API + "/v1/merchants/me/webhook-endpoints", {"mode": "sandbox", "url": WP + "/wp-json/waffle/v1/webhook"}, opener=jar)
if s != 201:
    raise SystemExit(f"endpoint: {s} {r}")
SECRET = r["signing_secret"]
KEY = os.environ["WAFFLE_KEY"]
setup = "update_option('woocommerce_waffle_settings',['enabled'=>'yes','title'=>'QRIS & Virtual Account','sandbox'=>'yes','sandbox_key'=>'%s','sandbox_secret'=>'%s','expiry'=>'60']);"
php(setup % (KEY, SECRET))

for label, sel in (("qris", {"channel": "qris"}), ("va", {"channel": "virtual_account", "va_bank": "PERMATA"})):
    o = order()
    res = start(o)
    c = state(o)["c"]
    check(f"{label}: redirects to the hosted page", res.get("redirect") == f"http://localhost:3001/pay/{c}", res)
    check(f"{label}: second start reuses the charge", start(o).get("redirect") == res.get("redirect"))
    pay(c, **sel)
    st = wait(o, "processing")
    check(f"{label}: webhook completes the order", st["s"] == "processing" and st["t"] == c, st)
    check(f"{label}: safety check is scheduled", php(f"echo as_has_scheduled_action('waffle_wc_check_order',[{o}],'waffle')?'y':'n';") == "y")
    n = st["n"]
    check(f"{label}: replayed webhook is harmless", deliver(SECRET, {"event": "charge.paid", "charge_id": c}) == 200 and state(o)["n"] == n)

sql("UPDATE merchants SET fee_bearer='customer' WHERE email='%s'" % os.environ["WAFFLE_EMAIL"])
try:
    o = order()
    start(o)
    pay(state(o)["c"], channel="qris")
    check("payer-borne fee still completes", wait(o, "processing")["s"] == "processing")
finally:
    sql("UPDATE merchants SET fee_bearer='merchant' WHERE email='%s'" % os.environ["WAFFLE_EMAIL"])

o = order()
start(o)
c = state(o)["c"]
php(f"$o=wc_get_order({o}); $o->set_total(90000); $o->save();")
pay(c, channel="qris")
check("amount changed after charge goes on-hold", wait(o, "on-hold")["s"] == "on-hold")

o = order()
start(o)
c = state(o)["c"]
php(f"wc_get_order({o})->update_status('cancelled');")
pay(c, channel="qris")
check("paid after cancel goes on-hold", wait(o, "on-hold")["s"] == "on-hold")

o = order()
start(o)
c1 = state(o)["c"]
php(f"$o=wc_get_order({o}); $o->set_total(60000); $o->save();")
start(o)
c2 = state(o)["c"]
check("changed amount makes a new charge", c1 != c2)
pay(c2, channel="qris")
wait(o, "processing")
pay(c1, channel="qris")
time.sleep(12)
st = state(o)
check("second payment never reopens a paid order", st["s"] == "processing" and st["t"] == c2, st)

o = order()
start(o)
c = state(o)["c"]
check("unpaid charge with a valid signature does not complete", deliver(SECRET, {"event": "charge.paid", "charge_id": c}) == 200 and state(o)["s"] == "pending")
check("bad signature refused", deliver("whsec_wrong", {"event": "charge.paid", "charge_id": c}) == 401)
check("stale signature refused", deliver(SECRET, {"event": "charge.paid", "charge_id": c}, int(time.time()) - 400) == 401)
php(setup % (KEY, "whsec_wrong"))
pay(c, channel="qris")
time.sleep(6)
check("order waits when the webhook is refused", state(o)["s"] == "pending")
php(f"Waffle_Orders::check({o});")
check("thank-you page check completes it", state(o)["s"] == "processing")
php(setup % (KEY, SECRET))

check("fractional total refused", php("add_filter('wc_get_price_decimals',fn()=>2); $o=wc_create_order(); $o->set_total(50000.5); echo null===Waffle_Orders::amount($o)?'y':'n';") == "y")
php("update_option('woocommerce_waffle_settings',['enabled'=>'yes','sandbox'=>'yes','sandbox_key'=>'wf_live_x','sandbox_secret'=>'x']);")
check("live key in sandbox mode disables the method", php("echo (new Waffle_Gateway())->is_available()?'y':'n';") == "n")
php(setup % (KEY, SECRET))
php("update_option('woocommerce_currency','USD');")
check("non-IDR store disables the method", php("echo (new Waffle_Gateway())->is_available()?'y':'n';") == "n")
php("update_option('woocommerce_currency','IDR');")
check("method available when configured", php("echo (new Waffle_Gateway())->is_available()?'y':'n';") == "y")

print(f"\n{len(fails)} failed")
sys.exit(1 if fails else 0)
