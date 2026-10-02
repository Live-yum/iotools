"""Canonicalize only CocoaPods-added object IDs; retain every project setting."""
import hashlib
import json
import re

TOKEN = re.compile(r'\s+|/\*.*?\*/|//[^\n]*|"(?:\\.|[^"\\])*"|[{}()=;,]|[^\s{}()=;,]+', re.S)
UUID = re.compile(r'^[A-F0-9]{24}$')


def parse(text):
    tokens = [m.group() for m in TOKEN.finditer(text)
              if not (m.group().isspace() or m.group().startswith(('/*', '//')))]
    pos = 0
    def take():
        nonlocal pos
        if pos >= len(tokens): raise ValueError('Truncated Xcode property list')
        token = tokens[pos]; pos += 1
        return token
    def value():
        token = take()
        if token == '{':
            result = {}
            while tokens[pos] != '}':
                key = take()
                key = json.loads(key) if key.startswith('"') else key
                if key in result or take() != '=': raise ValueError('Invalid/duplicate Xcode key')
                result[key] = value()
                if take() != ';': raise ValueError('Missing Xcode key terminator')
            take(); return result
        if token == '(':
            result = []
            while tokens[pos] != ')':
                result.append(value())
                if tokens[pos] == ',': take()
                elif tokens[pos] != ')': raise ValueError('Missing Xcode array separator')
            take(); return result
        if token in ('}', ')', '=', ';', ','): raise ValueError('Unexpected Xcode delimiter')
        return json.loads(token) if token.startswith('"') else token
    result = value()
    if pos != len(tokens) or not isinstance(result, dict): raise ValueError('Invalid Xcode root')
    return result


def normalized_project(original, generated):
    before, after = parse(original), parse(generated)
    original_ids = set(before['objects'])
    new_ids = set(after['objects']) - original_ids
    if not original_ids <= set(after['objects']): raise ValueError('Original project objects removed')
    if any(not UUID.fullmatch(key) for key in new_ids): raise ValueError('Unexpected generated object identifier')
    cache = {}
    def normalize(value, stack=()):
        if isinstance(value, dict): return {k: normalize(v, stack) for k, v in value.items()}
        if isinstance(value, list): return [normalize(v, stack) for v in value]
        if value in new_ids:
            if value in stack: raise ValueError('Unreviewed generated reference cycle')
            if value not in cache:
                data = json.dumps(normalize(after['objects'][value], stack + (value,)), sort_keys=True, separators=(',', ':'))
                cache[value] = 'generated:' + hashlib.sha256(data.encode()).hexdigest()
            return cache[value]
        return value
    result = {k: normalize(v) for k, v in after.items() if k != 'objects'}
    objects = {}
    for key, obj in after['objects'].items():
        identifier = normalize(key)
        if identifier in objects: raise ValueError('Duplicate normalized generated object')
        objects[identifier] = normalize(obj)
    result['objects'] = objects
    return result


def normalized_digest(original, generated):
    normalized = normalized_project(original, generated)
    return hashlib.sha256(json.dumps(normalized, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
