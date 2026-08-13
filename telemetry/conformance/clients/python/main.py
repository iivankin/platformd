import os

import sentry_sdk
from sentry_sdk.integrations.flask import FlaskIntegration

case = os.environ["CONFORMANCE_CASE"]
dsn = os.environ["SENTRY_DSN"]

if case == "python":
    sentry_sdk.init(dsn=dsn, shutdown_timeout=10, default_integrations=False)
    sentry_sdk.set_tag("conformance_case", case)
    try:
        raise RuntimeError("python conformance")
    except RuntimeError as error:
        sentry_sdk.capture_exception(error)
elif case == "flask":
    from flask import Flask

    sentry_sdk.init(dsn=dsn, integrations=[FlaskIntegration()], shutdown_timeout=10)
    sentry_sdk.set_tag("conformance_case", case)
    app = Flask(__name__)
    app.config["PROPAGATE_EXCEPTIONS"] = False

    @app.get("/fail")
    def fail():
        raise RuntimeError("flask conformance")

    app.test_client().get("/fail")
else:
    raise SystemExit(f"unsupported case: {case}")

sentry_sdk.flush(timeout=10)
