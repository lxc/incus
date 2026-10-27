rule 'Myst-MD031', 'Fenced code blocks should be surrounded by blank lines' do
  tags :code, :blank_lines
  aliases 'blanks-around-fences'
  check do |doc|
    errors = []
    # Some parsers (including kramdown) have trouble detecting fenced code
    # blocks without surrounding whitespace, so examine the lines directly.
    in_code = false
    fence = nil
    lines = [''] + doc.lines + ['']
    lines.each_with_index do |line, linenum|
      line.strip.match(/^(`{3,}|~{3,})/)
      unless Regexp.last_match(1) &&
             (
               !in_code ||
               (Regexp.last_match(1).slice(0, fence.length) == fence)
             )
        next
      end

      fence = in_code ? nil : Regexp.last_match(1)
      in_code = !in_code
      if (in_code && !(lines[linenum - 1].empty? || lines[linenum - 1].match(/^[:\-\*]*\s*\% /))) ||
         (!in_code && !(lines[linenum + 1].empty? || lines[linenum + 1].match(/^\s*:/)))
        errors << linenum
      end
    end
    errors
  end
end


rule 'Myst-IDs', 'MyST IDs should be preceded by a blank line' do
  check do |doc|
    errors = []
    ids = doc.matching_text_element_lines(/^\(.+\)=\s*$/)
    ids.each do |linenum|
      if (linenum > 1) && !doc.lines[linenum - 2].empty?
        errors << linenum
      end
    end
    errors.sort
  end
end
