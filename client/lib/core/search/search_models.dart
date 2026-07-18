import '../files/file_models.dart';
import '../files/file_path.dart';

const int maxSearchQueryCharacters = 100;
const int defaultSearchResultLimit = 100;
const int maxSearchResultLimit = 100;

String normalizeSearchQuery(String input) {
  final query = input.trim();
  if (query.isEmpty) {
    throw const FormatException('Search query is required');
  }
  if (query.runes.any((rune) => rune < 0x20 || rune == 0x7f)) {
    throw const FormatException(
      'Search query must not contain control characters',
    );
  }
  if (query.runes.length > maxSearchQueryCharacters) {
    throw const FormatException('Search query is too long');
  }
  return query;
}

int validateSearchResultLimit(int limit) {
  if (limit < 1 || limit > maxSearchResultLimit) {
    throw const FormatException(
      'Search result limit must be between 1 and 100',
    );
  }
  return limit;
}

final class SearchResultItem {
  const SearchResultItem._({
    required this.name,
    required this.path,
    required this.isDirectory,
    required this.size,
    required this.modifiedAt,
    required this.contentHash,
  });

  factory SearchResultItem.fromJson(Map<String, dynamic> json) {
    final name = validateLogicalName(_requiredString(json, 'name'));
    final path = _requiredCanonicalPath(json, 'path');
    if (path.split('/').last != name) {
      throw const FormatException('Search result name does not match its path');
    }

    final isDirectory = json['isDir'];
    if (isDirectory is! bool) {
      throw const FormatException('Invalid search result type');
    }
    final size = json['size'];
    if (size is! int || size < 0) {
      throw const FormatException('Invalid search result size');
    }

    final rawHash = json['hash'];
    final contentHash = switch (rawHash) {
      null => null,
      final String value when isLowercaseSha256Token(value) => value,
      _ => throw const FormatException('Invalid search result hash'),
    };

    return SearchResultItem._(
      name: name,
      path: path,
      isDirectory: isDirectory,
      size: size,
      modifiedAt: _requiredRfc3339Timestamp(json, 'modTime'),
      contentHash: contentHash,
    );
  }

  final String name;
  final String path;
  final bool isDirectory;
  final int size;
  final DateTime modifiedAt;
  final String? contentHash;

  String get parentPath {
    final separator = path.lastIndexOf('/');
    return separator <= 0 ? '/' : path.substring(0, separator);
  }
}

final class SearchListing {
  SearchListing._({
    required this.query,
    required List<SearchResultItem> results,
    required this.limit,
  }) : results = List<SearchResultItem>.unmodifiable(results);

  factory SearchListing.fromJson(
    Map<String, dynamic> json, {
    required String expectedQuery,
    required int limit,
  }) {
    final canonicalQuery = normalizeSearchQuery(expectedQuery);
    final canonicalLimit = validateSearchResultLimit(limit);
    if (_requiredString(json, 'query') != canonicalQuery) {
      throw const FormatException(
        'Search response query does not match the request',
      );
    }

    final rawResults = json['results'];
    if (rawResults is! List || rawResults.length > canonicalLimit) {
      throw const FormatException('Invalid search result list');
    }
    final results = rawResults
        .map((value) => SearchResultItem.fromJson(_requireMap(value)))
        .toList(growable: false);
    final paths = results.map((result) => result.path).toSet();
    if (paths.length != results.length) {
      throw const FormatException('Search result paths must be unique');
    }

    final count = json['count'];
    if (count is! int || count != results.length) {
      throw const FormatException(
        'Search result count does not match the list',
      );
    }
    return SearchListing._(
      query: canonicalQuery,
      results: results,
      limit: canonicalLimit,
    );
  }

  final String query;
  final List<SearchResultItem> results;
  final int limit;

  int get count => results.length;

  bool get reachedLimit => count == limit;
}

String _requiredCanonicalPath(Map<String, dynamic> json, String key) {
  final value = _requiredString(json, key);
  final normalized = normalizeLogicalPath(value, allowRoot: false);
  if (normalized != value) {
    throw FormatException('Invalid canonical $key');
  }
  return value;
}

DateTime _requiredRfc3339Timestamp(Map<String, dynamic> json, String key) {
  final value = _requiredString(json, key);
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T'
    r'(\d{2}):(\d{2}):(\d{2})'
    r'(?:\.(\d{1,9}))?'
    r'(?:Z|([+-])(\d{2}):(\d{2}))$',
  ).firstMatch(value);
  if (match == null) {
    throw FormatException('Invalid $key timestamp');
  }

  final year = int.parse(match.group(1)!);
  final month = int.parse(match.group(2)!);
  final day = int.parse(match.group(3)!);
  final hour = int.parse(match.group(4)!);
  final minute = int.parse(match.group(5)!);
  final second = int.parse(match.group(6)!);
  final offsetHour = int.parse(match.group(9) ?? '0');
  final offsetMinute = int.parse(match.group(10) ?? '0');
  if (year == 0 ||
      month < 1 ||
      month > 12 ||
      day < 1 ||
      day > _daysInMonth(year, month) ||
      hour > 23 ||
      minute > 59 ||
      second > 59 ||
      offsetHour > 23 ||
      offsetMinute > 59) {
    throw FormatException('Invalid $key timestamp');
  }

  final parsed = DateTime.tryParse(value);
  if (parsed == null) {
    throw FormatException('Invalid $key timestamp');
  }
  return parsed.toUtc();
}

int _daysInMonth(int year, int month) {
  return switch (month) {
    2 when year % 4 == 0 && (year % 100 != 0 || year % 400 == 0) => 29,
    2 => 28,
    4 || 6 || 9 || 11 => 30,
    _ => 31,
  };
}

String _requiredString(Map<String, dynamic> json, String key) {
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw FormatException('Missing or invalid $key');
  }
  return value;
}

Map<String, dynamic> _requireMap(Object? value) {
  if (value is Map<String, dynamic>) {
    return value;
  }
  if (value is Map) {
    return Map<String, dynamic>.from(value);
  }
  throw const FormatException('Expected a JSON object');
}
