#!/usr/bin/env python3
"""Radio Reference SOAP API sidecar for FAIF-Scanner.

FastAPI service on port 8200 that wraps the Radio Reference SOAP API
using zeep. Credentials are passed on every request, never stored.

RR WSDL method signatures (v=latest, s=doc):
  getUserData(authInfo) -> UserInfo
  getCountryList() -> Countries                          # no auth!
  getCountryInfo(coid, authInfo) -> CountryInfo{stateList}
  getStateInfo(stid, authInfo) -> StateInfo{countyList, agencyList, trsList}
  getCountyInfo(ctid, authInfo) -> CountyInfo{agencyList, trsList, cats}
  getTrsTalkgroups(sid, tgCid, tgTag, tgDec, authInfo) -> Talkgroups
  getCountyFreqsByTag(ctid, tag, authInfo) -> Freqs
"""

import logging
from datetime import datetime, timedelta, timezone
from typing import Optional

from dateutil import parser as dateparser
from fastapi import FastAPI, HTTPException, Query
from pydantic import BaseModel
from zeep import Client as SoapClient, Settings as SoapSettings
from zeep.exceptions import Fault

logger = logging.getLogger("rr-sidecar")

app = FastAPI(title="RR Sidecar", version="1.0.0")

WSDL_URL = "http://api.radioreference.com/soap2/?wsdl&v=latest&s=doc"

_soap_client: Optional[SoapClient] = None


def get_soap_client() -> SoapClient:
    global _soap_client
    if _soap_client is None:
        settings = SoapSettings(strict=False, xml_huge_tree=True)
        _soap_client = SoapClient(wsdl=WSDL_URL, settings=settings)
    return _soap_client


def build_auth(username: str, password: str, app_key: str):
    client = get_soap_client()
    auth_type = client.get_type("ns0:authInfo")
    return auth_type(
        username=username,
        password=password,
        appKey=app_key,
        version="latest",
        style="doc",
    )


def extract_field(obj, field_name: str, default: str = "") -> str:
    """Extract a field from a zeep result, checking attributes and _raw_elements."""
    val = getattr(obj, field_name, None)
    if val is not None:
        return str(val).strip()
    raw = getattr(obj, "_raw_elements", None)
    if raw:
        for elem in raw:
            local = elem.tag.split("}")[-1] if "}" in elem.tag else elem.tag
            if local == field_name:
                return (elem.text or "").strip()
    return default


def extract_fields(obj) -> dict:
    """Extract all fields from a zeep result or lxml Element into a plain dict."""
    from lxml import etree
    result = {}
    if obj is None:
        return result
    if isinstance(obj, str):
        return result
    # Handle raw lxml Element objects (from _raw_elements children)
    if isinstance(obj, etree._Element):
        for child in obj:
            local = child.tag.split("}")[-1] if "}" in child.tag else child.tag
            result[local] = (child.text or "").strip()
        return result
    # Handle zeep objects
    for attr in dir(obj):
        if attr.startswith("_"):
            continue
        val = getattr(obj, attr, None)
        if val is not None and not callable(val):
            result[attr] = val
    raw = getattr(obj, "_raw_elements", None)
    if raw:
        for elem in raw:
            local = elem.tag.split("}")[-1] if "}" in elem.tag else elem.tag
            if local not in result or result[local] is None:
                result[local] = (elem.text or "").strip()
    return result


def extract_list(obj, list_attr: str) -> list:
    """Extract a nested list from a zeep result (e.g. stateList, countyList).
    Returns the child 'item' elements as a list.
    Handles zeep objects, _raw_elements, and plain lxml Elements."""
    from lxml import etree
    lst = getattr(obj, list_attr, None)
    if lst is not None and hasattr(lst, "__iter__") and not isinstance(lst, str):
        items = list(lst)
        if items:
            return items
    # Check _raw_elements (zeep objects)
    raw = getattr(obj, "_raw_elements", None)
    if raw:
        for elem in raw:
            local = elem.tag.split("}")[-1] if "}" in elem.tag else elem.tag
            if local == list_attr:
                return list(elem)
    # Check direct children (lxml Element)
    if isinstance(obj, etree._Element):
        for child in obj:
            local = child.tag.split("}")[-1] if "}" in child.tag else child.tag
            if local == list_attr:
                return list(child)
    return []


def safe_str(val) -> str:
    if val is None:
        return ""
    return str(val).strip()


def safe_int(val, default: int = 0) -> int:
    if val is None:
        return default
    try:
        return int(val)
    except (ValueError, TypeError):
        return default


def safe_datetime(val) -> Optional[str]:
    if val is None:
        return None
    try:
        if isinstance(val, datetime):
            return val.isoformat()
        return dateparser.parse(str(val)).isoformat()
    except Exception:
        return None


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------

@app.get("/health")
def health(
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    client = get_soap_client()
    auth = build_auth(username, password, app_key)
    logger.info(f"health check for user={username}")
    try:
        result = client.service.getUserData(authInfo=auth)
    except Fault as e:
        logger.error(f"RR auth fault: {e.message}")
        raise HTTPException(status_code=401, detail=str(e.message))
    except Exception as e:
        logger.error(f"RR request error: {e}")
        raise HTTPException(status_code=502, detail=str(e))

    return {
        "username": extract_field(result, "username", username),
        "subscription": extract_field(result, "subType", "Feed Provider"),
        "expiry": extract_field(result, "subExpireDate", ""),
    }


@app.get("/countries")
def countries(
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """getCountryList() takes NO arguments."""
    client = get_soap_client()
    try:
        result = client.service.getCountryList()
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    if result:
        for c in result:
            f = extract_fields(c)
            items.append({
                "id": safe_int(f.get("coid", 0)),
                "name": safe_str(f.get("countryName", "")),
            })
    return items


@app.get("/states/{country_id}")
def states(
    country_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """getCountryInfo(coid, authInfo) -> CountryInfo with stateList."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)
    try:
        result = client.service.getCountryInfo(coid=country_id, authInfo=auth)
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    state_list = extract_list(result, "stateList")
    for s in state_list:
        f = extract_fields(s)
        items.append({
            "id": safe_int(f.get("stid", 0)),
            "name": safe_str(f.get("stateName", "")),
            "code": safe_str(f.get("stateCode", "")),
        })
    return items


@app.get("/counties/{state_id}")
def counties(
    state_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """getStateInfo(stid, authInfo) -> StateInfo with countyList."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)
    try:
        result = client.service.getStateInfo(stid=state_id, authInfo=auth)
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    county_list = extract_list(result, "countyList")
    for c in county_list:
        f = extract_fields(c)
        items.append({
            "id": safe_int(f.get("ctid", 0)),
            "name": safe_str(f.get("countyName", "")),
        })
    return items


@app.get("/agencies/{county_id}")
def agencies(
    county_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """getCountyInfo(ctid, authInfo) -> CountyInfo with agencyList."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)
    try:
        result = client.service.getCountyInfo(ctid=county_id, authInfo=auth)
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    agency_list = extract_list(result, "agencyList")
    for a in agency_list:
        f = extract_fields(a)
        items.append({
            "id": safe_int(f.get("aid", 0)),
            "name": safe_str(f.get("aName", "")),
            "type": safe_str(f.get("aType", "")),
        })
    return items


@app.get("/systems/{county_id}")
def systems(
    county_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """getCountyInfo(ctid, authInfo) -> CountyInfo with trsList."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)
    try:
        result = client.service.getCountyInfo(ctid=county_id, authInfo=auth)
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    trs_list = extract_list(result, "trsList")
    for s in trs_list:
        f = extract_fields(s)
        items.append({
            "id": safe_int(f.get("sid", 0)),
            "name": safe_str(f.get("sName", "")),
            "type": safe_str(f.get("sType", "")),
            "flavor": safe_str(f.get("sFlavor", "")),
            "voice": safe_str(f.get("sVoice", "")),
        })
    return items


# Well-known RR service tag IDs -> names
RR_TAG_MAP = {
    1: "Law Dispatch", 2: "Law Tac", 3: "Law Talk", 4: "Fire Dispatch",
    5: "Fire-Tac", 6: "Fire-Talk", 7: "EMS Dispatch", 8: "EMS-Tac",
    9: "EMS-Talk", 10: "Hospital", 11: "Multi-Dispatch", 12: "Multi-Tac",
    13: "Multi-Talk", 14: "Public Works", 15: "Utilities", 16: "Schools",
    17: "Security", 18: "Transportation", 19: "Aircraft", 20: "Railroad",
    21: "Military", 22: "Federal", 23: "Business", 24: "Ham",
    25: "Media", 26: "Corrections", 27: "Emergency Ops", 28: "Other",
    29: "Interop", 30: "Data", 31: "Deprecated",
    32: "Law Dispatch", 33: "Fire Dispatch", 34: "EMS Dispatch",
    35: "Multi-Dispatch", 36: "Other", 37: "Deprecated",
}


def resolve_tag_ids(tags_elem) -> list[str]:
    """Extract tag IDs from the tags XML element and resolve to names."""
    names = []
    if tags_elem is None:
        return names
    from lxml import etree
    if isinstance(tags_elem, etree._Element):
        for item in tags_elem:
            for sub in item:
                local = sub.tag.split("}")[-1] if "}" in sub.tag else sub.tag
                if local == "tagId" and sub.text:
                    tid = safe_int(sub.text)
                    if tid in RR_TAG_MAP:
                        names.append(RR_TAG_MAP[tid])
    return names


@app.get("/talkgroups/{system_id}")
def talkgroups(
    system_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """Fetches talkgroups AND categories, joins tgCid -> tgCname."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)

    # Fetch categories first to build tgCid -> tgCname map
    cat_map: dict[str, str] = {}
    try:
        cats = client.service.getTrsTalkgroupCats(sid=system_id, authInfo=auth)
        if cats:
            for cat in cats:
                cf = extract_fields(cat)
                cid = safe_str(cf.get("tgCid", ""))
                cname = safe_str(cf.get("tgCname", ""))
                if cid and cname:
                    cat_map[cid] = cname
    except Exception as e:
        logger.warning(f"Failed to fetch talkgroup categories for sid={system_id}: {e}")

    # Fetch talkgroups
    try:
        result = client.service.getTrsTalkgroups(
            sid=system_id, tgCid=0, tgTag=0, tgDec=0, authInfo=auth
        )
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    if result:
        from lxml import etree
        for tg in result:
            f = extract_fields(tg)
            # Map tgCid to category name
            tg_cid = safe_str(f.get("tgCid", ""))
            category = cat_map.get(tg_cid, "")
            # Resolve service tags from raw tags element
            tag_names: list[str] = []
            raw = getattr(tg, "_raw_elements", None)
            if raw:
                for elem in raw:
                    local = elem.tag.split("}")[-1] if "}" in elem.tag else elem.tag
                    if local == "tags":
                        tag_names = resolve_tag_ids(elem)
                        break
            tag_val = ", ".join(sorted(set(tag_names))) if tag_names else ""
            items.append({
                "decimal_id": safe_int(f.get("tgDec", 0)),
                "hex_id": safe_str(f.get("tgHex", "")),
                "alpha_tag": safe_str(f.get("tgAlpha", "")),
                "description": safe_str(f.get("tgDescr", "")),
                "mode": safe_str(f.get("tgMode", "")),
                "tag": tag_val,
                "category": category,
                "encrypted": safe_int(f.get("enc", 0)),
                "frequency": safe_int(f.get("tgFreq", 0)),
                "last_updated": safe_datetime(f.get("tgDate")),
            })
    return items


@app.get("/updates/{county_id}")
def updates(
    county_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """getCountyFreqsByTag(ctid, tag=0, authInfo) -> Freqs (tag=0 means all)."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)
    try:
        result = client.service.getCountyFreqsByTag(
            ctid=county_id, tag=0, authInfo=auth
        )
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    if result:
        for row in result:
            f = extract_fields(row)
            items.append({
                "frequency": safe_int(f.get("freq", 0)),
                "tg_id": safe_int(f.get("tgDec", 0)),
                "alpha_tag": safe_str(f.get("alpha", "")),
                "description": safe_str(f.get("descr", "")),
                "tone": safe_str(f.get("tone", "")),
                "service_tag": safe_str(f.get("tag", "")),
                "last_updated": safe_datetime(f.get("lastUpdated")),
            })
    return items


def parse_freq(row, category: str = "") -> dict:
    """Parse a freq object (from getSubcatFreqs or getCountyFreqsByTag) into
    a talkgroup-like dict matching the /talkgroups response shape."""
    from lxml import etree
    f = extract_fields(row)
    # Resolve service tags
    tag_names: list[str] = []
    raw = getattr(row, "_raw_elements", None)
    if raw:
        for elem in raw:
            local = elem.tag.split("}")[-1] if "}" in elem.tag else elem.tag
            if local == "tags":
                tag_names = resolve_tag_ids(elem)
                break
    tag_val = ", ".join(sorted(set(tag_names))) if tag_names else ""
    freq_out = safe_int(f.get("out", 0))
    return {
        "decimal_id": safe_int(f.get("fid", freq_out)),
        "hex_id": "",
        "alpha_tag": safe_str(f.get("alpha", "")) or safe_str(f.get("descr", "")),
        "description": safe_str(f.get("descr", "")),
        "mode": safe_str(f.get("mode", "")),
        "tag": tag_val,
        "category": category,
        "encrypted": safe_int(f.get("enc", 0)),
        "frequency": freq_out,
        "last_updated": safe_datetime(f.get("lastUpdated")),
    }


@app.get("/frequencies/{county_id}")
def frequencies(
    county_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """Conventional frequencies for a county. Uses getCountyInfo to get
    subcategories, then getSubcatFreqs for each subcategory."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)

    # Get county info with category tree
    try:
        county_info = client.service.getCountyInfo(ctid=county_id, authInfo=auth)
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    cats = extract_list(county_info, "cats")
    for cat_elem in cats:
        cf = extract_fields(cat_elem)
        cat_name = safe_str(cf.get("cName", ""))
        subcats = extract_list(cat_elem, "subcats")
        for subcat_elem in subcats:
            sf = extract_fields(subcat_elem)
            scid = safe_int(sf.get("scid", 0))
            sc_name = safe_str(sf.get("scName", ""))
            full_cat = f"{cat_name} / {sc_name}" if cat_name and sc_name else (cat_name or sc_name or "Uncategorized")
            if scid == 0:
                continue
            try:
                freqs = client.service.getSubcatFreqs(scid=scid, authInfo=auth)
                if freqs:
                    for freq in freqs:
                        items.append(parse_freq(freq, category=full_cat))
            except Exception as e:
                logger.warning(f"Failed getSubcatFreqs scid={scid}: {e}")
    return items


@app.get("/agency-freqs/{agency_id}")
def agency_freqs(
    agency_id: int,
    username: str = Query(...),
    password: str = Query(...),
    app_key: str = Query(...),
):
    """Frequencies for a specific agency. Uses getAgencyInfo to get
    subcategories, then getSubcatFreqs for each."""
    client = get_soap_client()
    auth = build_auth(username, password, app_key)

    try:
        agency_info = client.service.getAgencyInfo(aid=agency_id, authInfo=auth)
    except Fault as e:
        raise HTTPException(status_code=502, detail=str(e.message))
    except Exception as e:
        raise HTTPException(status_code=502, detail=str(e))

    items = []
    cats = extract_list(agency_info, "cats")
    for cat_elem in cats:
        cf = extract_fields(cat_elem)
        cat_name = safe_str(cf.get("cName", ""))
        subcats = extract_list(cat_elem, "subcats")
        for subcat_elem in subcats:
            sf = extract_fields(subcat_elem)
            scid = safe_int(sf.get("scid", 0))
            sc_name = safe_str(sf.get("scName", ""))
            full_cat = f"{cat_name} / {sc_name}" if cat_name and sc_name else (cat_name or sc_name or "Uncategorized")
            if scid == 0:
                continue
            try:
                freqs = client.service.getSubcatFreqs(scid=scid, authInfo=auth)
                if freqs:
                    for freq in freqs:
                        items.append(parse_freq(freq, category=full_cat))
            except Exception as e:
                logger.warning(f"Failed getSubcatFreqs scid={scid}: {e}")
    return items


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=8200)
