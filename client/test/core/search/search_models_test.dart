import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/search/search_models.dart';

void main() {
  group('search request validation', () {
    test('trims a valid query and accepts supported limits', () {
      expect(normalizeSearchQuery('  家庭 report  '), '家庭 report');
      expect(validateSearchResultLimit(1), 1);
      expect(validateSearchResultLimit(100), 100);
    });

    test('rejects blank, control-character, oversized queries and limits', () {
      expect(() => normalizeSearchQuery('   '), throwsFormatException);
      expect(() => normalizeSearchQuery('report\n2026'), throwsFormatException);
      expect(
        normalizeSearchQuery(List<String>.filled(100, '文').join()),
        isNotEmpty,
      );
      expect(
        () => normalizeSearchQuery(List<String>.filled(101, '文').join()),
        throwsFormatException,
      );
      expect(() => validateSearchResultLimit(0), throwsFormatException);
      expect(() => validateSearchResultLimit(101), throwsFormatException);
    });
  });

  group('SearchListing', () {
    test('decodes a strict immutable response', () {
      final listing = SearchListing.fromJson(
        _listingJson(),
        expectedQuery: 'report',
        limit: 50,
      );

      expect(listing.query, 'report');
      expect(listing.count, 2);
      expect(listing.reachedLimit, isFalse);
      expect(listing.results.first.name, 'report.pdf');
      expect(listing.results.first.parentPath, '/documents');
      expect(listing.results.first.modifiedAt, DateTime.utc(2026, 7, 19, 4));
      expect(
        listing.results.first.contentHash,
        List<String>.filled(64, 'a').join(),
      );
      expect(listing.results.last.parentPath, '/');
      expect(() => listing.results.clear(), throwsUnsupportedError);
    });

    test('reports when the exact result limit is reached', () {
      final json = _listingJson()
        ..['results'] = [(_listingJson()['results']! as List<dynamic>).first]
        ..['count'] = 1;

      final listing = SearchListing.fromJson(
        json,
        expectedQuery: 'report',
        limit: 1,
      );

      expect(listing.reachedLimit, isTrue);
    });

    test(
      'rejects mismatched query, count, duplicates, and oversized lists',
      () {
        final invalidCases = <void Function(Map<String, dynamic>)>[
          (json) => json['query'] = 'different',
          (json) => json['count'] = 1,
          (json) {
            final results = json['results']! as List<dynamic>;
            results[1] = Map<String, dynamic>.from(
              results.first as Map<String, dynamic>,
            );
          },
          (json) {
            final results = json['results']! as List<dynamic>;
            results.add(Map<String, dynamic>.from(results.first as Map));
            json['count'] = 3;
          },
        ];

        for (var index = 0; index < invalidCases.length; index++) {
          final json = _listingJson();
          invalidCases[index](json);
          expect(
            () => SearchListing.fromJson(
              json,
              expectedQuery: 'report',
              limit: index == 3 ? 2 : 50,
            ),
            throwsFormatException,
            reason: json.toString(),
          );
        }
      },
    );

    test('rejects malformed result fields and non-canonical paths', () {
      final invalidCases = <void Function(Map<String, dynamic>)>[
        (json) => _firstResult(json)['name'] = 'different.pdf',
        (json) => _firstResult(json)['path'] = 'documents/report.pdf',
        (json) => _firstResult(json)['path'] = '/documents//report.pdf',
        (json) => _firstResult(json)['isDir'] = 0,
        (json) => _firstResult(json)['size'] = -1,
        (json) => _firstResult(json)['size'] = 42.0,
        (json) => _firstResult(json)['modTime'] = '2026-07-19T12:00:00',
        (json) => _firstResult(json)['modTime'] = '2026-02-30T12:00:00Z',
        (json) => _firstResult(json)['hash'] = 'ABC',
      ];

      for (final invalidate in invalidCases) {
        final json = _listingJson();
        invalidate(json);
        expect(
          () =>
              SearchListing.fromJson(json, expectedQuery: 'report', limit: 50),
          throwsFormatException,
          reason: json.toString(),
        );
      }
    });
  });
}

Map<String, dynamic> _listingJson() => <String, dynamic>{
  'query': 'report',
  'results': <Map<String, dynamic>>[
    <String, dynamic>{
      'name': 'report.pdf',
      'path': '/documents/report.pdf',
      'isDir': false,
      'size': 1048576,
      'modTime': '2026-07-19T12:00:00+08:00',
      'hash': List<String>.filled(64, 'a').join(),
    },
    <String, dynamic>{
      'name': 'reports',
      'path': '/reports',
      'isDir': true,
      'size': 0,
      'modTime': '2026-07-18T12:00:00Z',
    },
  ],
  'count': 2,
};

Map<String, dynamic> _firstResult(Map<String, dynamic> json) {
  return (json['results']! as List<dynamic>).first as Map<String, dynamic>;
}
