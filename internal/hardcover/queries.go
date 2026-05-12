package hardcover

const queryMe = `query Me { me { id account_privacy_setting_id } }`

const queryStatuses = `query Statuses { user_book_statuses { id status } }`

const queryBookBySlug = `query FindBookBySlug($slug: String!) {
  books(where: { slug: { _eq: $slug } }, limit: 1) {
    id title slug cached_image
    book_series { series { name } position }
    default_ebook_edition { id pages reading_format_id }
    default_physical_edition { id pages reading_format_id }
  }
}`

const queryEditionByISBN13 = `query FindEditionByISBN13($isbn: String!) {
  editions(where: { isbn_13: { _eq: $isbn } }, limit: 1) {
    id book_id pages isbn_13 reading_format_id
    book { id title slug default_ebook_edition { id pages reading_format_id } }
  }
}`

const queryEditionByISBN10 = `query FindEditionByISBN10($isbn: String!) {
  editions(where: { isbn_10: { _eq: $isbn } }, limit: 1) {
    id book_id pages isbn_10 reading_format_id
    book { id title slug default_ebook_edition { id pages reading_format_id } }
  }
}`

const querySearchBooks = `query SearchBooks($query: String!) {
  search(query: $query, query_type: "Book", per_page: 5, page: 1) { ids }
}`

const editionFields = `id title pages isbn_13 isbn_10 asin edition_format reading_format_id cached_image publisher { name }`

var queryBooksByIDs = `query BooksByIDs($ids: [Int!]!) {
  books(where: { id: { _in: $ids } }) {
    id title slug cached_image
    contributions { author { name } }
    book_series { series { name } position }
    default_ebook_edition { ` + editionFields + ` }
    default_physical_edition { ` + editionFields + ` }
  }
}`

const queryUserBook = `query GetUserBook($bookId: Int!, $userId: Int!) {
  user_books(where: { book_id: { _eq: $bookId }, user_id: { _eq: $userId } }, limit: 1) {
    id book_id status_id edition_id user_id
    user_book_reads { id started_at finished_at progress_pages edition_id }
  }
}`

const mutationInsertUserBook = `mutation InsertUserBook($object: UserBookCreateInput!) {
  insert_user_book(object: $object) {
    error
    user_book { id book_id status_id edition_id }
  }
}`

const mutationUpdateUserBook = `mutation UpdateUserBook($id: Int!, $object: UserBookUpdateInput!) {
  update_user_book(id: $id, object: $object) {
    error
    user_book { id status_id edition_id }
  }
}`

const mutationInsertUserBookRead = `mutation InsertUserBookRead($id: Int!, $read: DatesReadInput!) {
  insert_user_book_read(user_book_id: $id, user_book_read: $read) {
    error
    user_book_read { id started_at finished_at progress_pages }
  }
}`

const mutationUpdateUserBookRead = `mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
  update_user_book_read(id: $id, object: $object) {
    error
    user_book_read { id progress_pages finished_at }
  }
}`
