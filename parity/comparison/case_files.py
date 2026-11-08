"""Read a route's case file and expand it into the requests to be sent.

This is the whole reason adding a route is a data entry. A case file says which
route it is, what a valid request to it looks like, and which shared matrices to
run against it; the matrices in parity/matrices are the question every route is
asked — no credential, a bad one, an agent's, a cookie, a workspace the caller
is not in, one that does not exist, an id that is malformed or past int32 or
past int64, and an origin that is foreign. A route's own peculiarities go in its
`cases`, which are sent exactly as written.

A label is built from the matrix and the variant, so a failure names the
question that failed rather than a number.
"""

from __future__ import annotations

import json
import os
import urllib.parse


def _load(path: str) -> dict:
    with open(path, encoding="utf-8") as text:
        return json.load(text)


def _path_for(template: str, parameters: dict, query: dict) -> str:
    path = template
    for name, value in parameters.items():
        path = path.replace("{" + name + "}", str(value))
    if query:
        pairs = [(name, str(value)) for name, value in query.items() if value is not None]
        # quote_via=str so a case can ask a query parameter the way a client
        # would spell it, including the spellings that are the point of the
        # test.
        path += "?" + urllib.parse.urlencode(pairs, quote_via=urllib.parse.quote)
    return path


class Suite:
    """Every case file, with the matrices they draw on."""

    def __init__(self, cases_directory: str, matrices_directory: str):
        self._cases = cases_directory
        self._matrices = {
            name[: -len(".json")]: _load(os.path.join(matrices_directory, name))
            for name in sorted(os.listdir(matrices_directory))
            if name.endswith(".json")
        }

    def files(self) -> list[str]:
        return [
            os.path.join(self._cases, name)
            for name in sorted(os.listdir(self._cases))
            if name.endswith(".json")
        ]

    def expand(self, path: str) -> tuple[str, list[dict]]:
        """The declared route, and every concrete request the file asks for."""
        declared = _load(path)
        route = declared.get("route", "")
        method = route.split(" ", 1)[0] if route else "GET"
        template = route.split(" ", 1)[1] if " " in route else ""
        parameters = declared.get("parameters", {})
        query = declared.get("query", {})
        inherited = {
            "tables": declared.get("tables", []),
            "queues": declared.get("queues", []),
            "mint": declared.get("mint", False),
        }
        credential = declared.get("as", "owner")

        requests: list[dict] = []
        for named in declared.get("matrices", []):
            name, _, parameter = named.partition(":")
            matrix = self._matrices.get(name)
            if matrix is None:
                raise KeyError(f"{path}: no matrix named {name!r}")
            for variant in matrix["variants"]:
                request = {
                    "label": f"{name} · {variant['label']}",
                    "method": method,
                    "path": _path_for(template, parameters, query),
                    "as": credential,
                    "headers": dict(declared.get("headers", {})),
                    **inherited,
                }
                if "body" in declared:
                    request["body"] = declared["body"]
                target = matrix["target"]
                if target == "credential":
                    request["as"] = variant["as"]
                elif target == "headers":
                    request["headers"].update(variant.get("headers", {}))
                elif target == "request":
                    # A variant that is a credential and headers together,
                    # which is what a cross-site write is.
                    request["as"] = variant.get("as", request["as"])
                    request["headers"].update(variant.get("headers", {}))
                elif target == "path":
                    if not parameter:
                        raise KeyError(f"{path}: matrix {name!r} needs a parameter name")
                    request["path"] = _path_for(
                        template, {**parameters, parameter: variant["value"]}, query)
                elif target == "query":
                    if not parameter:
                        raise KeyError(f"{path}: matrix {name!r} needs a parameter name")
                    request["path"] = _path_for(
                        template, parameters, {**query, parameter: variant["value"]})
                else:
                    raise KeyError(f"{path}: matrix {name!r} has an unknown target")
                requests.append(request)

        for body in declared.get("bodies", []):
            requests.append({
                "label": f"bodies · {body['label']}",
                "method": method,
                "path": _path_for(template, parameters, query),
                "as": body.get("as", credential),
                "headers": {**declared.get("headers", {}), **body.get("headers", {})},
                "body": body["body"],
                **{**inherited, **{k: body[k] for k in ("tables", "queues", "mint") if k in body}},
            })

        for case in declared.get("cases", []):
            request = {**inherited, **case}
            request.setdefault("method", method)
            request.setdefault("as", credential)
            request.setdefault("headers", {})
            if "path" not in request:
                request["path"] = _path_for(
                    template, {**parameters, **request.pop("parameters", {})},
                    {**query, **request.pop("query", {})})
            request["label"] = case.get("label") or case.get("name") or request["path"]
            requests.append(request)

        return route, requests


def with_credential(request: dict, credentials: dict) -> dict:
    """The request as it goes on the wire, with the named credential applied."""
    named = request.get("as", "none")
    if named not in credentials:
        raise KeyError(f"no credential named {named!r}")
    headers = dict(credentials[named])
    headers.update(request.get("headers", {}))
    sent = {"method": request["method"], "path": request["path"], "headers": headers}
    if "body" in request:
        sent["body"] = request["body"]
    return sent
