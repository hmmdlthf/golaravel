/// The server answered with an error status.
class GoLaravelHttpError implements Exception {
  GoLaravelHttpError(this.status, this.code, [this.message = '', this.retryAfter]);

  /// HTTP status code.
  final int status;

  /// The `error` field of the JSON body, or "http_error".
  final String code;
  final String message;

  /// Seconds, from the Retry-After header.
  final int? retryAfter;

  /// Worth retrying later: overload, maintenance, rate limits.
  bool get transient => status >= 500 || status == 429 || status == 408;

  @override
  String toString() => 'GoLaravelHttpError($status, $code${message.isEmpty ? '' : ': $message'})';
}

/// The server could not be reached (offline, DNS, TLS, timeout).
class GoLaravelNetworkError implements Exception {
  GoLaravelNetworkError(this.cause);

  final Object cause;

  @override
  String toString() => 'GoLaravelNetworkError($cause)';
}
