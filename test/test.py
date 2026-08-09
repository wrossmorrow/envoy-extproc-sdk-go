from dataclasses import dataclass, asdict
from json import dumps, JSONDecodeError

import requests


@dataclass
class TesterRequestBody:
    append_request_headers: dict[str, str] | None = None
    add_request_headers: dict[str, str] | None = None
    overwrite_request_headers: dict[str, str] | None = None
    remove_request_headers: list[str] | None = None
    clear_request_body: bool | None = None
    replace_request_body: str | None = None

    append_response_headers: dict[str, str] | None = None
    add_response_headers: dict[str, str] | None = None
    overwrite_response_headers: dict[str, str] | None = None
    remove_response_headers: list[str] | None = None
    clear_response_body: bool | None = None
    replace_response_body: str | None = None

    cancel_request: bool | None = None
    cancel_request_status: int | None = None
    cancel_request_body: str | None = None

    def __post_init__(self):
        if self.clear_request_body and self.replace_request_body:
            raise ValueError("Cannot set both clear_request_body and replace_request_body")
        if self.clear_response_body and self.replace_response_body:
            raise ValueError("Cannot set both clear_response_body and replace_response_body")


@dataclass
class TesterResponseBody:
    Datetime: str
    Server: str
    Hostname: str
    Method: str
    Path: str
    Query: dict[str, str]
    Headers: dict[str, list[str]]
    Body: str
    Duration: int
    Status: int


def asdict_not_null(obj: dataclass) -> dict[str, object]:
    return {k: v for k, v in asdict(obj).items() if v is not None}


def make_request(
    headers: dict[str, str],
    body: TesterRequestBody,
) -> tuple[int, dict[str, str],TesterResponseBody | None]:
    
    response = requests.post(
        "http://localhost:8080/test",
        headers=headers,
        json=asdict_not_null(body),
    )

    try:
        response_obj = TesterResponseBody(**response.json())
    except JSONDecodeError:
        response_obj = None
    
    return response.status_code, dict(response.headers), response_obj


def show(status_code: int, headers: dict[str, str], response_obj: TesterResponseBody | None) -> None:
    if response_obj is None:
        print(f"{status_code} {dumps(headers, indent=2)} <no response body>")
    else:
        print(f"{status_code} {dumps(headers, indent=2)} {dumps(asdict(response_obj), indent=2)}")


# "cases" below here


# should just work
results = make_request(
    headers={},
    body=TesterRequestBody(),
)
show(*results)

# should have results[2].Body == "" or None
results = make_request(
    headers={},
    body=TesterRequestBody(
        clear_request_body=True,
    ),
)
show(*results)

# should have various complicated header outcomes in the body
results = make_request(
    headers={
        "x-overwrite": "old value",
        "x-remove_me": "true",
    },
    body=TesterRequestBody(
        append_request_headers={"x-append": "append_value"},
        add_request_headers={"x-add": "add_value"},
        overwrite_request_headers={"x-overwrite": "new value"},
        remove_request_headers=["x-remove_me"],
    ),
)
show(*results)

# should have various complicated header outcomes in the response headers
results = make_request(
    headers={},
    body=TesterRequestBody(
        append_response_headers={"x-append": "append_value"},
        add_response_headers={"x-add": "add_value"},
        overwrite_response_headers={"content-type": "text/plain"},
        remove_response_headers=["date"],
    ),
)
show(*results)

# should have no response body
results = make_request(
    headers={},
    body=TesterRequestBody(
        clear_response_body=True,
    ),
)
show(*results)
