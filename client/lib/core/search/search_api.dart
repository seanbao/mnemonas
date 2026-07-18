import 'package:dio/dio.dart';

import '../network/api_client.dart';
import '../network/api_error.dart';
import 'search_models.dart';

final class SearchApi {
  const SearchApi(this._client);

  final ApiClient _client;

  Future<ApiResponse<SearchListing>> search(
    String query, {
    int limit = defaultSearchResultLimit,
    CancelToken? cancelToken,
  }) {
    final canonicalQuery = normalizeSearchQuery(query);
    final canonicalLimit = validateSearchResultLimit(limit);
    return _client.requestEnvelope<SearchListing>(
      '/api/v1/search',
      queryParameters: <String, dynamic>{
        'q': canonicalQuery,
        'limit': canonicalLimit,
      },
      cancelToken: cancelToken,
      decode: (data) => SearchListing.fromJson(
        _requireMap(data),
        expectedQuery: canonicalQuery,
        limit: canonicalLimit,
      ),
    );
  }
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
