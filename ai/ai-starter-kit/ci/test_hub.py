import sys
import requests
from packaging.version import Version as V


def test_hub_up(hub_url):
    r = requests.get(hub_url)
    r.raise_for_status()
    print("JupyterHub up.")


def test_api_root(hub_url):
    """
    Tests the hub api's root endpoint (/). The hub's version should be returned.

    A typical jupyterhub logging response to this test:

        [I 2019-09-25 12:03:12.051 JupyterHub log:174] 200 GET /hub/api (test@127.0.0.1) 9.57ms
    """
    r = requests.get(hub_url + "/hub/api")
    r.raise_for_status()
    info = r.json()
    version = info["version"]
    assert V("4") <= V(version) <= V("5.5"), f"version {version} must be between 4 and 5.5"
    print("JupyterHub Rest API is working.")


def test_hub_login(hub_url):
    """
    Tests the hub dummy authenticator login credentials. Login credentials retrieve
    from /jupyter_config/config.yaml. After successfully login, user will be
    redirected to /hub/spawn.
    """
    username, password = "user", "sneakypass"
    session = requests.Session()

    response = session.get(hub_url + "/hub/login")
    response.raise_for_status()

    auth_params = {}
    if "_xsrf" in session.cookies:
        auth_params = {"_xsrf": session.cookies["_xsrf"]}

    response = session.post(
        hub_url + "/hub/login",
        params=auth_params,
        data={"username": username, "password": password},
        allow_redirects=True,
    )
    response.raise_for_status()
    assert (hub_url + "/hub/spawn-pending/user") in response.url, f"unexpected response url: got {response.url}, expected {hub_url}/hub/spawn-pending/user"
    print("JupyterHub login success.")


hub_url = "http://" + sys.argv[1]

test_hub_up(hub_url)
test_api_root(hub_url)
test_hub_login(hub_url)
