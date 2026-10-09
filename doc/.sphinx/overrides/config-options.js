// SPDX-License-Identifier: Apache-2.0
// From canonical-sphinx-extensions bc648473619f93fdfefb9ae8becbe7d00dad675a.
// Modified to remove an invalid no-op requestAnimationFrame call.
$(document).ready(function() {

    /* Hide all details except if the URL has an anchor for one */
    $('.configoption div.details').prop("hidden","until-found");

    if ($(location).attr('hash')) {
        $('div#'+$.escapeSelector($(location).attr('hash').substr(1))+" > .details").removeAttr("hidden");
    };

    /* Add icons to expand/collapse all options for one section */
    $('.configoption').before("<div class=\"expand-collapse\"><span class=\"expand-all\" title=\"Expand all\">⤋</span><span class=\"collapse-all\" title=\"Collapse all\">⤊</span></div>");
    $('.configoption ~ .configoption').prev(".expand-collapse").remove();

    /* Make the option lines expandable */
    $('.configoption div.basicinfo').click(function() {
        if ($(this).nextAll('.configoption .details').first().prop("hidden")) {
            $(this).nextAll('.configoption .details').first().removeAttr("hidden");
        }
        else {
            $(this).nextAll('.configoption .details').first().prop("hidden","until-found");
        }
    });

    /* Expand/collapse all options in a section */
    $('.expand-all').click(function() {
        $(this).parent().nextAll('.configoption').find('.details').removeAttr("hidden");
    });

    $('.collapse-all').click(function() {
        $(this).parent().nextAll('.configoption').find('.details').prop("hidden","until-found");
    });

    /* When clicking a config reference, expand it automatically */
    $('.configref').click(function() {
        if ($(this).attr('href').substr(0,1) == "#") {
            $('.configoption div.details').prop("hidden","until-found");
            $('#'+$.escapeSelector($(this).attr('href').substr(1))+" .details").removeAttr("hidden");
        };
    });

    /* If searching in hidden content is not supported, add an
       "Expand all options" link at the top of the page. */
    if (!('onbeforematch' in document.body)) {
        $('.main .content article h1:first-of-type').after("<div id=\"expand-options\">⤋ Expand all options</div>");

        $('#expand-options').click(function() {
            $('.configoption div.details').removeAttr("hidden");

        });
    };


})
