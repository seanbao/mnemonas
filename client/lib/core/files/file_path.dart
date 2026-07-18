String normalizeLogicalPath(String input, {bool allowRoot = true}) {
  if (input.runes.any((rune) => rune < 0x20 || rune == 0x7f)) {
    throw const FormatException(
      'File path must not contain control characters',
    );
  }
  if (input.contains(r'\')) {
    throw const FormatException('File path must use forward slashes');
  }

  final absolute = input.startsWith('/') ? input : '/$input';
  final segments = absolute.split('/').where((segment) => segment.isNotEmpty);
  final normalizedSegments = <String>[];
  for (final segment in segments) {
    if (segment == '.' || segment == '..') {
      throw const FormatException('File path must not contain dot segments');
    }
    normalizedSegments.add(segment);
  }

  if (normalizedSegments.isEmpty) {
    if (!allowRoot) {
      throw const FormatException('Root is not valid for this operation');
    }
    return '/';
  }
  return '/${normalizedSegments.join('/')}';
}

String encodeLogicalPath(String input, {bool allowRoot = true}) {
  final normalized = normalizeLogicalPath(input, allowRoot: allowRoot);
  if (normalized == '/') {
    return '';
  }
  return normalized.substring(1).split('/').map(Uri.encodeComponent).join('/');
}

String validateLogicalName(String input) {
  if (input.isEmpty) {
    throw const FormatException('File name is required');
  }
  if (input == '.' || input == '..') {
    throw const FormatException('File name must not be a dot segment');
  }
  if (input.contains('/') || input.contains(r'\')) {
    throw const FormatException('File name must contain exactly one segment');
  }
  if (input.runes.any((rune) => rune < 0x20 || rune == 0x7f)) {
    throw const FormatException(
      'File name must not contain control characters',
    );
  }
  return input;
}

String uniqueLogicalName(String original, Set<String> reservedNames) {
  final name = validateLogicalName(original);
  if (!reservedNames.contains(name)) {
    return name;
  }

  final dot = name.lastIndexOf('.');
  final hasExtension = dot > 0 && dot < name.length - 1;
  final stem = hasExtension ? name.substring(0, dot) : name;
  final extension = hasExtension ? name.substring(dot) : '';
  for (var index = 1; ; index++) {
    final candidate = '$stem ($index)$extension';
    if (!reservedNames.contains(candidate)) {
      return candidate;
    }
  }
}
