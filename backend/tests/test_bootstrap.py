from fastapi.testclient import TestClient


def test_health_is_public_and_versioned():
    from astrorder.main import app

    with TestClient(app) as client:
        response = client.get('/health')
        assert response.status_code == 200
        assert response.json() == {
            'status': 'ok', 'service': 'astrorder', 'protocol_version': 1
        }


def test_private_bootstrap_is_not_open_by_default():
    from astrorder.main import app

    with TestClient(app) as client:
        assert client.get('/api/v1/bootstrap').status_code in (401, 403, 503)
